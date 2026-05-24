// Package review implements `ant review`: a Bubble Tea TUI that walks the staged
// diffs `ant fix` left, one finding at a time, and mutates nothing on disk
// (review-interaction.md, TECHSPEC §7 — "Mutates code? No"). Its only side effect
// is marking each diff accepted/skipped (a Mark persisted via the Store) so a
// later `ant apply` lands exactly the accepted set.
//
// The model + Program runner live here in the engine (not cmd/ant) because the
// Bubble Tea program uses goroutines/channels the CLI boundary forbids; the CLI
// calls Run with a Store + runID and renders the result. The model is updated
// immutably (a new state per Update) per the Go coding-style rules.
package review

import (
	"context"
	"errors"
	"fmt"
	"io"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/events"
	"github.com/gitpcl/ant/internal/engine/stage"
)

// Marker persists a reviewer's decision on the staged record at index. The
// stage.Area satisfies it; tests inject a fake. Keeping it an interface lets the
// model persist marks without importing the concrete Store (small interface,
// defined where used — Go idiom).
type Marker interface {
	Mark(index int, mark engine.Mark) error
}

// Options parameterizes a review session.
type Options struct {
	RunID  string
	Ascii  bool
	Color  bool
	Marker Marker
}

// Run loads the staged records for runID and either prints the empty-state
// screen (no staged diffs) or launches the walk TUI. It returns when the user
// quits. A load error is operational (exit 2); the empty state is success
// (exit 0). It owns the Bubble Tea program (goroutines), keeping the CLI thin.
//
// store is the staging source (stage.Area over the Store); marker persists marks
// (normally the same Area). w is where the TUI renders (stdout for a TTY).
func Run(ctx context.Context, w io.Writer, area *stage.Area, opts Options) error {
	records, err := area.ListRecords()
	if err != nil {
		// A missing run is "nothing to review", not an operational failure: the
		// user ran `ant review` before any `ant fix`. Show the empty-state screen
		// and exit 0 (review-interaction.md §5.1). Any other error is operational.
		if errors.Is(err, engine.ErrRunNotFound) {
			m := newModel(nil, opts.RunID, nil, opts.Ascii, opts.Color)
			_, werr := io.WriteString(w, m.emptyView())
			return werr
		}
		return fmt.Errorf("%w: load staged diffs for review: %v", engine.ErrOperational, err)
	}

	marker := opts.Marker
	if marker == nil {
		marker = area
	}

	m := newModel(records, opts.RunID, marker, opts.Ascii, opts.Color)
	if m.phase == phaseEmpty {
		// No walk — print the static empty-state screen and exit 0
		// (review-interaction.md §5.1). No TUI program for the empty case.
		_, werr := io.WriteString(w, m.emptyView())
		return werr
	}

	prog := tea.NewProgram(m, tea.WithOutput(w), tea.WithContext(ctx))
	_, rerr := prog.Run()
	return rerr
}

// phase is the review session's screen state (review-interaction.md §8).
type phase int

const (
	phaseWalking     phase = iota // walking items, the main screen
	phaseConfirmQuit              // q pressed with pending items (§5.3)
	phaseEnd                      // last item decided/passed (§5.2)
	phaseEmpty                    // no staged diffs (§5.1)
)

// reviewItem is one staged fix being reviewed: its provenance triple plus the
// mutable mark (review-interaction.md §8). The mark mirrors the persisted Store
// mark; the model updates it in memory and persists each change immediately.
type reviewItem struct {
	finding engine.Finding
	diff    engine.ProposedDiff
	verify  engine.VerifyResult
	mark    engine.Mark
}

// model is the review TUI state (review-interaction.md §8). Updated immutably.
type model struct {
	glyphs events.Glyphs
	styles styles

	runID  string
	marker Marker

	items  []reviewItem
	cursor int

	showFull bool // diff expanded (d)
	showExpl bool // explain pane shown (e)
	showHelp bool // ? help overlay
	scroll   int  // diff scroll offset (expanded)
	phase    phase
}

// compile-time assertion that model satisfies tea.Model.
var _ tea.Model = model{}

// newModel builds the initial review model from the staged records. An empty set
// enters phaseEmpty so Run prints the static screen instead of a walk (§5.1).
func newModel(records []engine.StagedRecord, runID string, marker Marker, ascii, color bool) model {
	items := make([]reviewItem, 0, len(records))
	for _, r := range records {
		items = append(items, reviewItem{finding: r.Finding, diff: r.Diff, verify: r.Verify, mark: r.Mark})
	}
	ph := phaseWalking
	if len(items) == 0 {
		ph = phaseEmpty
	}
	return model{
		glyphs: events.GlyphSet(ascii),
		styles: newStyles(color),
		runID:  runID,
		marker: marker,
		items:  items,
		phase:  ph,
	}
}

// Init has no startup command — review is purely keyboard-driven, no animation.
func (m model) Init() tea.Cmd { return nil }

// Update handles keypresses, applying the six verbs + navigation aliases
// (review-interaction.md §1, §4). It returns a new model per the immutable-update
// rule. Mark persistence happens synchronously on a/s so a crash never loses a
// decision (§1).
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch m.phase {
	case phaseConfirmQuit:
		return m.updateConfirmQuit(key)
	case phaseEnd:
		return m.updateEnd(key)
	default:
		return m.updateWalking(key)
	}
}

// updateWalking handles the main review screen's keys.
func (m model) updateWalking(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "ctrl+c":
		return m, tea.Quit // force quit; persisted marks are kept (§4)
	case "a", "A":
		return m.decide(engine.MarkAccepted)
	case "s", "S":
		return m.decide(engine.MarkSkipped)
	case "n", "N":
		return m.advance(), nil
	case "p", "P":
		if m.cursor > 0 {
			m.cursor--
			m.resetPanes()
		}
		return m, nil
	case "d", "D":
		m.showFull = !m.showFull
		m.scroll = 0
		return m, nil
	case "e", "E":
		m.showExpl = !m.showExpl
		return m, nil
	case "?":
		m.showHelp = !m.showHelp
		return m, nil
	case "up", "k":
		if m.showFull && m.scroll > 0 {
			m.scroll--
		}
		return m, nil
	case "down", "j":
		if m.showFull {
			m.scroll++
		}
		return m, nil
	case "g":
		m.scroll = 0
		return m, nil
	case "G":
		m.scroll = m.maxScroll()
		return m, nil
	case "q", "Q":
		if m.hasPending() {
			m.phase = phaseConfirmQuit
			return m, nil
		}
		return m, tea.Quit
	}
	return m, nil
}

// updateConfirmQuit handles the quit-with-pending confirm prompt (§5.3).
func (m model) updateConfirmQuit(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "q", "Q", "ctrl+c":
		return m, tea.Quit // pending stay pending (unapplied); accepted stay marked
	case "r", "R":
		m.phase = phaseWalking
		m.cursor = m.firstPending()
		m.resetPanes()
		return m, nil
	case "a", "A":
		// Accept-all: the only bulk action, reachable only through this two-step
		// confirm so it can never fire accidentally (§5.3).
		for i := range m.items {
			if m.items[i].mark == engine.MarkPending {
				m.items[i].mark = engine.MarkAccepted
				_ = m.marker.Mark(i, engine.MarkAccepted)
			}
		}
		return m, tea.Quit
	}
	return m, nil
}

// updateEnd handles the End screen (§5.2): p returns to the first non-accepted
// item to finish off pending/skipped; q quits.
func (m model) updateEnd(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "q", "Q", "ctrl+c":
		return m, tea.Quit
	case "p", "P":
		m.cursor = m.firstNonAccepted()
		m.phase = phaseWalking
		m.resetPanes()
		return m, nil
	}
	return m, nil
}

// decide marks the current item and persists it, then auto-advances (§1).
func (m model) decide(mark engine.Mark) (tea.Model, tea.Cmd) {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return m, nil
	}
	m.items[m.cursor].mark = mark
	if err := m.marker.Mark(m.cursor, mark); err != nil {
		// Persistence failure is surfaced by leaving the in-memory mark but not
		// crashing the TUI; the next apply re-reads the Store, which is the
		// source of truth. (A hard error here would lose the whole session.)
		_ = err
	}
	return m.advance(), nil
}

// advance moves to the next item, or to the End screen when past the last (§5.2).
// It does NOT change marks — `n` and the post-decide auto-advance share it.
func (m model) advance() model {
	if m.cursor+1 < len(m.items) {
		m.cursor++
		m.resetPanes()
		return m
	}
	m.phase = phaseEnd
	return m
}

// resetPanes collapses the diff/explain panes when moving between items so each
// item starts in the scannable default view (review-interaction.md §7).
func (m *model) resetPanes() {
	m.showFull = false
	m.showExpl = false
	m.scroll = 0
}

// hasPending reports whether any item is still undecided (§5.3 quit guard).
func (m model) hasPending() bool {
	for _, it := range m.items {
		if it.mark == engine.MarkPending {
			return true
		}
	}
	return false
}

// firstPending returns the index of the first pending item, or 0.
func (m model) firstPending() int {
	for i, it := range m.items {
		if it.mark == engine.MarkPending {
			return i
		}
	}
	return 0
}

// firstNonAccepted returns the first item not yet accepted (pending or skipped)
// so End-screen `p` lands on something still actionable (§5.2).
func (m model) firstNonAccepted() int {
	for i, it := range m.items {
		if it.mark != engine.MarkAccepted {
			return i
		}
	}
	return 0
}

// counts tallies accepted/skipped/pending for the End screen (§5.2).
func (m model) counts() (accepted, skipped, pending int) {
	for _, it := range m.items {
		switch it.mark {
		case engine.MarkAccepted:
			accepted++
		case engine.MarkSkipped:
			skipped++
		default:
			pending++
		}
	}
	return
}
