package review

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gitpcl/ant/internal/engine"
)

// fakeMarker records persisted marks so tests can assert accept/skip persist
// immediately (review-interaction.md §1).
type fakeMarker struct{ marks map[int]engine.Mark }

func newFakeMarker() *fakeMarker { return &fakeMarker{marks: map[int]engine.Mark{}} }

func (f *fakeMarker) Mark(index int, mark engine.Mark) error {
	f.marks[index] = mark
	return nil
}

func sampleRecords(n int) []engine.StagedRecord {
	recs := make([]engine.StagedRecord, 0, n)
	for i := 0; i < n; i++ {
		recs = append(recs, engine.StagedRecord{
			Finding: engine.Finding{Species: "n+1-query", File: "internal/db/query.go", Span: engine.Span{StartLine: 88}, Severity: engine.SeverityHigh, Message: "N+1 query inside loop over users"},
			Diff: engine.ProposedDiff{
				Files:     []engine.FileDiff{{Path: "internal/db/query.go", Patch: "@@ -86,3 +86,2 @@\n a\n-bad\n+good\n"}},
				Fixer:     "pi (qwen2.5-coder)",
				Rationale: "Replaced the per-iteration query with a single batched Preload.",
			},
			Verify: engine.VerifyResult{Passed: true, Checks: []engine.CheckResult{
				{Name: "diff-bounded", Passed: true}, {Name: "compile", Passed: true},
				{Name: "tests:affected", Passed: true, Detail: "import-graph"}}},
			Mark: engine.MarkPending,
		})
	}
	return recs
}

func key(s string) tea.KeyMsg {
	if len(s) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func press(m model, keys ...string) model {
	var cur tea.Model = m
	for _, k := range keys {
		cur, _ = cur.Update(key(k))
	}
	return cur.(model)
}

// TestEmptyState asserts no records → phaseEmpty + the static screen (§5.1).
func TestEmptyState(t *testing.T) {
	m := newModel(nil, "r", newFakeMarker(), false, false)
	if m.phase != phaseEmpty {
		t.Fatalf("empty set should be phaseEmpty, got %v", m.phase)
	}
	if !strings.Contains(m.emptyView(), "No staged diffs were found") {
		t.Errorf("empty view missing the no-diffs message:\n%s", m.emptyView())
	}
}

// TestProvenancePanelRendersTriple asserts the panel shows species + fixer +
// passed verifiers (PRD §6.4, §3).
func TestProvenancePanelRendersTriple(t *testing.T) {
	m := newModel(sampleRecords(1), "a1f9c2", newFakeMarker(), false, false)
	view := m.View()
	for _, want := range []string{"PROVENANCE", "n+1-query", "HIGH", "pi (qwen2.5-coder)", "diff-bounded", "compile", "tests:affected", "import-graph"} {
		if !strings.Contains(view, want) {
			t.Errorf("provenance panel missing %q:\n%s", want, view)
		}
	}
}

// TestAcceptMarksAndAutoAdvances asserts `a` marks accepted, persists, and moves
// to the next item (§1).
func TestAcceptMarksAndAutoAdvances(t *testing.T) {
	fm := newFakeMarker()
	m := newModel(sampleRecords(3), "r", fm, false, false)
	m = press(m, "a")
	if fm.marks[0] != engine.MarkAccepted {
		t.Errorf("accept should persist MarkAccepted at index 0, got %v", fm.marks[0])
	}
	if m.cursor != 1 {
		t.Errorf("accept should auto-advance to cursor 1, got %d", m.cursor)
	}
	if m.items[0].mark != engine.MarkAccepted {
		t.Errorf("item 0 in-memory mark should be accepted")
	}
}

// TestSkipMarksAndAutoAdvances asserts `s` marks skipped and advances (§1).
func TestSkipMarksAndAutoAdvances(t *testing.T) {
	fm := newFakeMarker()
	m := newModel(sampleRecords(2), "r", fm, false, false)
	m = press(m, "s")
	if fm.marks[0] != engine.MarkSkipped {
		t.Errorf("skip should persist MarkSkipped at index 0, got %v", fm.marks[0])
	}
	if m.cursor != 1 {
		t.Errorf("skip should auto-advance, cursor = %d", m.cursor)
	}
}

// TestNextLeavesPending asserts `n` advances WITHOUT changing the mark (§1).
func TestNextLeavesPending(t *testing.T) {
	fm := newFakeMarker()
	m := newModel(sampleRecords(2), "r", fm, false, false)
	m = press(m, "n")
	if len(fm.marks) != 0 {
		t.Errorf("next must not persist any mark, got %v", fm.marks)
	}
	if m.items[0].mark != engine.MarkPending {
		t.Errorf("next must leave the item pending")
	}
	if m.cursor != 1 {
		t.Errorf("next should advance to cursor 1, got %d", m.cursor)
	}
}

// TestExplainTogglesRationale asserts `e` shows ProposedDiff.Rationale (§2.2).
func TestExplainTogglesRationale(t *testing.T) {
	m := newModel(sampleRecords(1), "r", newFakeMarker(), false, false)
	if strings.Contains(m.View(), "batched Preload") {
		t.Fatal("rationale should be hidden by default (diff view)")
	}
	m = press(m, "e")
	if !strings.Contains(m.View(), "EXPLAIN") || !strings.Contains(m.View(), "batched Preload") {
		t.Errorf("explain toggle should show the rationale:\n%s", m.View())
	}
}

// TestExplainEmptyRationaleFallback asserts a deterministic fix with no rationale
// shows the fallback line (§2.2).
func TestExplainEmptyRationaleFallback(t *testing.T) {
	recs := sampleRecords(1)
	recs[0].Diff.Rationale = ""
	recs[0].Diff.Fixer = "deterministic (delete-match)"
	m := newModel(recs, "r", newFakeMarker(), false, false)
	m = press(m, "e")
	if !strings.Contains(m.View(), "No rationale recorded") || !strings.Contains(m.View(), "deterministic (delete-match)") {
		t.Errorf("empty-rationale fallback missing:\n%s", m.View())
	}
}

// TestDiffToggleExpands asserts `d` flips the diff pane and the bar reflects it (§4).
func TestDiffToggleExpands(t *testing.T) {
	m := newModel(sampleRecords(1), "r", newFakeMarker(), false, false)
	m = press(m, "d")
	if !m.showFull {
		t.Error("d should expand the diff")
	}
	if !strings.Contains(m.View(), "d diff (expanded)") {
		t.Errorf("verb bar should show the expanded state:\n%s", m.View())
	}
}

// TestLastDiffReachesEndScreen asserts deciding the final item shows the End
// screen rather than wrapping (§5.2).
func TestLastDiffReachesEndScreen(t *testing.T) {
	m := newModel(sampleRecords(2), "a1f9c2", newFakeMarker(), false, false)
	m = press(m, "a", "a") // decide both
	if m.phase != phaseEnd {
		t.Fatalf("after the last decision the phase should be End, got %v", m.phase)
	}
	view := m.View()
	for _, want := range []string{"REVIEW COMPLETE", "accepted 2", "Run `ant apply`"} {
		if !strings.Contains(view, want) {
			t.Errorf("end screen missing %q:\n%s", want, view)
		}
	}
}

// TestQuitWithPendingConfirms asserts q with pending items shows the confirm
// prompt and quit-anyway exits leaving pending unapplied (§5.3).
func TestQuitWithPendingConfirms(t *testing.T) {
	m := newModel(sampleRecords(2), "r", newFakeMarker(), false, false)
	m = press(m, "a") // accept item 0, advance to 1 (pending)
	m2, _ := m.Update(key("q"))
	mm := m2.(model)
	if mm.phase != phaseConfirmQuit {
		t.Fatalf("q with a pending item should show confirm-quit, got %v", mm.phase)
	}
	if !strings.Contains(mm.View(), "still pending") {
		t.Errorf("confirm screen should warn about pending diffs:\n%s", mm.View())
	}
	// quit anyway → tea.Quit (a command); pending stays pending.
	_, cmd := mm.Update(key("q"))
	if cmd == nil {
		t.Error("quit-anyway should return tea.Quit")
	}
}

// TestAcceptAllPending asserts the confirm screen's `a` accepts every pending
// item (the only bulk action, §5.3).
func TestAcceptAllPending(t *testing.T) {
	fm := newFakeMarker()
	m := newModel(sampleRecords(3), "r", fm, false, false)
	// Skip item 0, then quit with 2 pending → confirm → accept-all.
	m = press(m, "s")
	m2, _ := m.Update(key("q"))
	mm := m2.(model)
	mm2, _ := mm.Update(key("a"))
	_ = mm2
	if fm.marks[1] != engine.MarkAccepted || fm.marks[2] != engine.MarkAccepted {
		t.Errorf("accept-all should accept all pending: %v", fm.marks)
	}
	if fm.marks[0] != engine.MarkSkipped {
		t.Errorf("accept-all must not change an already-skipped item: %v", fm.marks)
	}
}

// TestQuitNoPendingExitsImmediately asserts q with no pending quits without the
// confirm prompt (§5.3).
func TestQuitNoPendingExitsImmediately(t *testing.T) {
	m := newModel(sampleRecords(1), "r", newFakeMarker(), false, false)
	m = press(m, "s") // decide the only item → phaseEnd
	// From End, q quits.
	_, cmd := m.Update(key("q"))
	if cmd == nil {
		t.Error("q from the End screen should quit")
	}
}

// TestNoColorDropsANSI asserts color-off rendering has no ANSI escapes (§6).
func TestNoColorDropsANSI(t *testing.T) {
	m := newModel(sampleRecords(1), "r", newFakeMarker(), false, false)
	if strings.Contains(m.View(), "\x1b[") {
		t.Errorf("color-disabled review view must contain no ANSI escapes:\n%q", m.View())
	}
}

// TestAsciiGlyphs asserts --ascii replaces the heavy glyphs in the end screen (§6).
func TestAsciiGlyphs(t *testing.T) {
	m := newModel(sampleRecords(1), "r", newFakeMarker(), true, false)
	m = press(m, "a") // → End screen showing the accepted/skipped/pending glyphs
	if strings.ContainsAny(m.View(), "✔⊘◷●") {
		t.Errorf("ascii mode must not emit heavy glyphs:\n%s", m.View())
	}
}
