package events

import (
	"context"
	"fmt"
	"io"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gitpcl/ant/internal/engine"
)

// RenderTUI drains a subscription through a Bubble Tea program that renders the
// live colony view (docs/design/colony-view.md). It is ONE consumer of the same
// bus RenderJSON and RenderHuman consume — the TUI invents no state beyond what
// the events carry (colony-view.md §0, §5). The CLI attaches this renderer only
// when stdout is a TTY and --json is absent; otherwise it attaches RenderJSON.
//
// RenderTUI owns the Bubble Tea program (which internally uses goroutines), which
// is exactly why it lives in the engine and not cmd/ant — the boundary test
// forbids those constructs in the CLI layer. It returns when the bus closes (the
// producing run finished) and the program has rendered the final summary.
//
// workers is the --concurrency value (the lane count); ascii forces the ASCII
// glyph fallback; color toggles ANSI styling (off under NO_COLOR).
func RenderTUI(ctx context.Context, w io.Writer, sub *Subscription, workers int, ascii, color bool) error {
	m := newColonyModel(workers, ascii, color)
	prog := tea.NewProgram(
		m,
		tea.WithOutput(w),
		tea.WithContext(ctx),
		// No input handling beyond `q`/Ctrl-C: the live view is non-interactive
		// except for quitting (colony-view.md §2.1 footer). WithoutCatchPanics off
		// so a renderer panic surfaces rather than corrupting the terminal.
	)

	// Pump bus events into the program as messages. The program's Run reads them
	// in its own loop; when the bus closes we send a sentinel so the program can
	// finalize on run.end and then quit. This goroutine is engine-side machinery.
	go func() {
		for ev := range sub.C {
			prog.Send(eventMsg(ev))
		}
		prog.Send(busClosedMsg{})
	}()

	_, err := prog.Run()
	return err
}

// eventMsg wraps a bus Event as a Bubble Tea message so Update can fold it into
// the model. tea.Msg is an interface{}; this typed wrapper keeps Update's type
// switch explicit.
type eventMsg Event

// busClosedMsg signals the bus has no more events (the run completed and the bus
// was closed). The model quits the program on it.
type busClosedMsg struct{}

// spinnerTickMsg advances the working-lane spinner. It is the only animated
// element (colony-view.md §3.1) and the redraw cadence floor (§7).
type spinnerTickMsg struct{}

// laneState is one of the four states a worker lane can be in (colony-view.md
// §1). QUEUED is never laned (it is a header counter only), but the type carries
// it for completeness of the state machine.
type laneState int

const (
	stateQueued laneState = iota
	stateWorking
	stateVerified
	stateSkipped
)

// lane is one worker's row in Region B, keyed by AntID and reused as the worker
// cycles through findings (colony-view.md §1, §8). Its fields are all derived
// from bus events; startedAt is the renderer's own clock for the elapsed timer.
type lane struct {
	antID      int
	state      laneState
	species    string // remembered from ant.start Finding (ant.verified does not repeat it)
	file       string
	startLine  int
	startedAt  time.Time
	failCheck  string    // ant.skipped FailedCheck.Name
	applied    string    // apply.done sub-line (only under `ant fix --apply`)
	collapseAt time.Time // when a VERIFIED lane should collapse (1.5s after verify)
}

// failureRow is a pinned row in the persistent Failures panel (colony-view.md
// §3.3). Appended on every ant.skipped and NEVER cleared for the run.
type failureRow struct {
	species string
	file    string
	line    int
	check   string
	detail  string
}

// colonyModel is the in-memory state the renderer maintains, all derived from
// the bus (colony-view.md §8). It is updated immutably (a new model per Update)
// per the Go coding-style rules.
type colonyModel struct {
	glyphs Glyphs
	pal    palette

	runID       string
	scopeRoot   string
	species     []string
	workerCount int

	lanes      map[int]*lane
	laneOrder  []int // stable lane render order (first-seen AntID order)
	queueDepth int
	found      int
	verified   int
	skipped    int
	inFlight   int
	highestSev engine.Severity
	failures   []failureRow

	spinnerFrame int
	now          time.Time

	done bool
	end  RunEndPayload
}

// compile-time assertion that colonyModel satisfies tea.Model.
var _ tea.Model = (*colonyModel)(nil)

// newColonyModel builds the initial model. workers (the lane budget) comes from
// --concurrency; a value < 1 is normalized to 1 so the view always has at least
// one lane slot (mirrors the colony pool's NumCPU normalization).
func newColonyModel(workers int, ascii, color bool) *colonyModel {
	if workers < 1 {
		workers = 1
	}
	return &colonyModel{
		glyphs:      GlyphSet(ascii),
		pal:         newPalette(color),
		workerCount: workers,
		lanes:       map[int]*lane{},
		highestSev:  engine.SeverityUnknown,
		now:         time.Now(),
	}
}

// Init starts the spinner tick loop (the only animation, colony-view.md §3.1).
func (m *colonyModel) Init() tea.Cmd {
	return spinnerTick()
}

// spinnerTick schedules the next 100ms spinner advance (colony-view.md §3.1, §7).
func spinnerTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

// Update folds one message into the model. Each bus event drives exactly the
// transition colony-view.md §8 specifies; the spinner tick advances the
// animation and refreshes elapsed timers / collapses expired VERIFIED lanes.
func (m *colonyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinnerTickMsg:
		m.spinnerFrame++
		m.now = time.Now()
		m.collapseExpiredLanes()
		return m, spinnerTick()

	case busClosedMsg:
		// The run finished and the bus closed. If run.end already arrived we are
		// done; quit so Run returns and the CLI proceeds. (run.end normally
		// precedes the close, so done is already true.)
		m.done = true
		return m, tea.Quit

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
		return m, nil

	case eventMsg:
		m.now = time.Now()
		m.applyEvent(Event(msg))
		if Event(msg).Type == TypeRunEnd {
			// On run.end the live regions freeze and the view switches to the
			// static summary (colony-view.md §4). We do NOT quit here: the bus
			// close (busClosedMsg) drives the quit, so the summary is the last
			// frame painted and remains on screen.
			return m, nil
		}
		return m, nil
	}
	return m, nil
}

// applyEvent mutates the model per colony-view.md §8 transitions. It is the
// single place events change state; View is a pure projection of the result.
func (m *colonyModel) applyEvent(ev Event) {
	switch ev.Type {
	case TypeRunStart:
		if ev.RunStart != nil {
			m.runID = ev.RunStart.RunID
			m.scopeRoot = ev.RunStart.Scope.Root
			m.species = ev.RunStart.Scope.Species
		}
	case TypeDetectFinding:
		if ev.DetectFinding != nil {
			m.found++
			m.queueDepth++
			if ev.DetectFinding.Finding.Severity > m.highestSev {
				m.highestSev = ev.DetectFinding.Finding.Severity
			}
		}
	case TypeAntStart:
		if ev.AntStart != nil {
			m.openLane(*ev.AntStart)
		}
	case TypeAntVerified:
		if ev.AntVerified != nil {
			m.markVerified(*ev.AntVerified)
		}
	case TypeAntSkipped:
		if ev.AntSkipped != nil {
			m.markSkipped(*ev.AntSkipped)
		}
	case TypeApplyDone:
		if ev.ApplyDone != nil {
			m.attachApplied(*ev.ApplyDone)
		}
	case TypeRunEnd:
		if ev.RunEnd != nil {
			m.done = true
			m.end = *ev.RunEnd
		}
	}
}

// openLane upserts a lane to WORKING for an ant.start (colony-view.md §8). The
// queue depth drops and in-flight rises; the lane remembers the Finding's
// species/file because ant.verified does not repeat them (§3.2).
func (m *colonyModel) openLane(p AntStartPayload) {
	if m.queueDepth > 0 {
		m.queueDepth--
	}
	m.inFlight++
	l, ok := m.lanes[p.AntID]
	if !ok {
		l = &lane{antID: p.AntID}
		m.lanes[p.AntID] = l
		m.laneOrder = append(m.laneOrder, p.AntID)
	}
	l.state = stateWorking
	l.species = p.Finding.Species
	l.file = p.Finding.File
	l.startLine = p.Finding.Span.StartLine
	l.startedAt = m.now
	l.failCheck = ""
	l.applied = ""
	l.collapseAt = time.Time{}
}

// markVerified moves a lane to VERIFIED (colony-view.md §3.2, §8). The verified
// COUNT is permanent; the lane lingers 1.5s then collapses to free the slot.
func (m *colonyModel) markVerified(p AntVerifiedPayload) {
	m.verified++
	if m.inFlight > 0 {
		m.inFlight--
	}
	l, ok := m.lanes[p.AntID]
	if !ok {
		return
	}
	l.state = stateVerified
	if len(p.Diff.Files) > 0 {
		l.file = p.Diff.Files[0].Path
	}
	l.collapseAt = m.now.Add(1500 * time.Millisecond)
}

// markSkipped moves a lane to SKIPPED and PINS a row to the Failures panel
// (colony-view.md §3.3, §8). The failures slice is never cleared for the run.
func (m *colonyModel) markSkipped(p AntSkippedPayload) {
	m.skipped++
	if m.inFlight > 0 {
		m.inFlight--
	}
	if l, ok := m.lanes[p.AntID]; ok {
		l.state = stateSkipped
		l.failCheck = p.FailedCheck.Name
		l.file = p.Finding.File
		l.startLine = p.Finding.Span.StartLine
		l.species = p.Finding.Species
	}
	detail := p.FailedCheck.Detail
	if detail == "" {
		detail = p.Reason
	}
	if detail == "" {
		detail = "verification failed"
	}
	m.failures = append(m.failures, failureRow{
		species: p.Finding.Species,
		file:    p.Finding.File,
		line:    p.Finding.Span.StartLine,
		check:   p.FailedCheck.Name,
		detail:  detail,
	})
}

// attachApplied adds an "applied" sub-line to the lane whose verified path
// matches the apply.done path (colony-view.md §3.5). Only relevant under
// `ant fix --apply`.
func (m *colonyModel) attachApplied(p ApplyDonePayload) {
	for _, id := range m.laneOrder {
		l := m.lanes[id]
		if l.state == stateVerified && l.file == p.Path {
			if p.Branch == "" {
				l.applied = fmt.Sprintf("current branch (%s)", short(p.Commit))
			} else {
				l.applied = fmt.Sprintf("%s (%s)", p.Branch, short(p.Commit))
			}
			return
		}
	}
}

// collapseExpiredLanes frees VERIFIED lanes whose 1.5s linger has elapsed
// (colony-view.md §3.2) so the worker slot is reusable by the next ant.start.
func (m *colonyModel) collapseExpiredLanes() {
	for _, l := range m.lanes {
		if l.state == stateVerified && !l.collapseAt.IsZero() && m.now.After(l.collapseAt) {
			// Collapse to QUEUED-equivalent (free slot): the lane is reused on the
			// next ant.start for this AntID. We keep the entry but mark it idle.
			l.state = stateQueued
		}
	}
}
