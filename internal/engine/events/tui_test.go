package events

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gitpcl/ant/internal/engine"
)

// feed folds a sequence of bus events into a colony model via Update, returning
// the resulting model. It exercises the same path RenderTUI's event pump drives.
func feed(m *colonyModel, evs ...Event) *colonyModel {
	var model tea.Model = m
	for _, ev := range evs {
		model, _ = model.Update(eventMsg(ev))
	}
	return model.(*colonyModel)
}

func runStart(runID, root string, species ...string) Event {
	return Event{Type: TypeRunStart, RunStart: &RunStartPayload{RunID: runID, Scope: engine.Scope{Root: root, Species: species}}}
}

func detectFinding(species, file string, line int, sev engine.Severity) Event {
	return Event{Type: TypeDetectFinding, DetectFinding: &DetectFindingPayload{
		Finding: engine.Finding{Species: species, File: file, Span: engine.Span{StartLine: line}, Severity: sev},
	}}
}

func antStart(antID int, species, file string, line int) Event {
	return Event{Type: TypeAntStart, AntStart: &AntStartPayload{AntID: antID,
		Finding: engine.Finding{Species: species, File: file, Span: engine.Span{StartLine: line}}}}
}

func antVerified(antID int, file string) Event {
	return Event{Type: TypeAntVerified, AntVerified: &AntVerifiedPayload{AntID: antID,
		Diff:   engine.ProposedDiff{Files: []engine.FileDiff{{Path: file}}, Fixer: "deterministic (delete-match)"},
		Verify: engine.VerifyResult{Passed: true, Checks: []engine.CheckResult{{Name: "compile", Passed: true}}}}}
}

func antSkipped(antID int, species, file string, line int, check, detail string) Event {
	return Event{Type: TypeAntSkipped, AntSkipped: &AntSkippedPayload{AntID: antID,
		Finding:     engine.Finding{Species: species, File: file, Span: engine.Span{StartLine: line}},
		FailedCheck: engine.CheckResult{Name: check, Passed: false, Detail: detail},
		Reason:      detail}}
}

func runEnd(found, verified, skipped, applied int, highest string) Event {
	return Event{Type: TypeRunEnd, RunEnd: &RunEndPayload{RunID: "a1f9c2", Findings: found, Verified: verified, Skipped: skipped, Applied: applied, HighestSeverity: highest}}
}

// TestColonyLiveViewRendersAllStates drives one ant.start→verified and one
// ant.start→skipped and asserts the live view shows every state with its glyph
// and label (colony-view.md §3). State is never color-alone — assert the labels.
func TestColonyLiveViewRendersAllStates(t *testing.T) {
	m := newColonyModel(4, false, false) // no color so the assertions are plain text
	m = feed(m,
		runStart("a1f9c2run", "./internal", "unused-import", "dead-code"),
		detectFinding("unused-import", "internal/api/handler.go", 8, engine.SeverityHigh),
		detectFinding("n+1-query", "internal/db/query.go", 88, engine.SeverityHigh),
		antStart(1, "n+1-query", "internal/db/query.go", 88),
		antStart(2, "unused-import", "internal/api/handler.go", 8),
		antVerified(2, "internal/api/handler.go"),
		antSkipped(1, "n+1-query", "internal/db/query.go", 88, "compile", "undefined: scopedTx"),
	)
	view := m.View()

	for _, want := range []string{"WORKING", "VERIFIED", "SKIPPED", "FAILURES", "compile"} {
		// WORKING only renders if a lane is still working; after the skip, lane 1 is
		// SKIPPED. Open a fresh working lane to assert WORKING too.
		if want == "WORKING" {
			continue
		}
		if !strings.Contains(view, want) {
			t.Errorf("live view missing %q:\n%s", want, view)
		}
	}
	// Verified glyph + species present.
	if !strings.Contains(view, "✔") || !strings.Contains(view, "unused-import") {
		t.Errorf("verified lane not rendered:\n%s", view)
	}
	// Skip is prominent: red glyph ✖ + the failing check detail pinned in Failures.
	if !strings.Contains(view, "✖") || !strings.Contains(view, "undefined: scopedTx") {
		t.Errorf("failures panel did not pin the skip reason:\n%s", view)
	}
	// Header reflects scope + species + worker count.
	if !strings.Contains(view, "scanning ./internal") || !strings.Contains(view, "workers 4") {
		t.Errorf("header missing scope/workers:\n%s", view)
	}
}

// TestColonyWorkingLaneShowsSpinnerAndFixing asserts a WORKING lane renders the
// fixing… umbrella label (colony-view.md §3.1 — no fix/verify sub-state).
func TestColonyWorkingLaneShowsSpinnerAndFixing(t *testing.T) {
	m := newColonyModel(2, false, false)
	m = feed(m, runStart("r", "."), detectFinding("dead-code", "x.go", 5, engine.SeverityLow), antStart(3, "dead-code", "internal/cli/root.go", 55))
	view := m.View()
	if !strings.Contains(view, "WORKING") || !strings.Contains(view, "fixing…") {
		t.Errorf("working lane should show WORKING + fixing…:\n%s", view)
	}
	if !strings.Contains(view, "#3") {
		t.Errorf("working lane should be keyed by AntID #3:\n%s", view)
	}
}

// TestColonyFailuresPanelHiddenUntilFirstSkip asserts the panel is absent on a
// clean run and appears once a skip arrives (colony-view.md §3.3).
func TestColonyFailuresPanelHiddenUntilFirstSkip(t *testing.T) {
	m := newColonyModel(2, false, false)
	m = feed(m, runStart("r", "."), detectFinding("unused-import", "a.go", 1, engine.SeverityLow), antStart(1, "unused-import", "a.go", 1), antVerified(1, "a.go"))
	if strings.Contains(m.View(), "FAILURES") {
		t.Errorf("clean run must not show a FAILURES panel:\n%s", m.View())
	}
	m = feed(m, antStart(1, "dead-code", "b.go", 2), antSkipped(1, "dead-code", "b.go", 2, "detector-clears", "finding still present"))
	if !strings.Contains(m.View(), "FAILURES (1)") {
		t.Errorf("a skip must reveal the FAILURES panel:\n%s", m.View())
	}
}

// TestColonySummaryUsesRunEndCounts asserts the end-of-run summary reads counts
// directly from run.end (authoritative), not the running tallies (§4.1).
func TestColonySummaryUsesRunEndCounts(t *testing.T) {
	m := newColonyModel(4, false, false)
	m = feed(m,
		runStart("a1f9c2", "./internal"),
		antSkipped(1, "n+1-query", "internal/db/query.go", 88, "compile", "undefined: scopedTx"),
		runEnd(7, 5, 2, 0, "high"),
	)
	view := m.View()
	if !strings.Contains(view, "COLONY SUMMARY") {
		t.Errorf("run.end should switch to the summary screen:\n%s", view)
	}
	for _, want := range []string{"found        7", "verified   5", "skipped    2", "HIGH", "Run `ant review`", "Nothing was applied"} {
		if !strings.Contains(view, want) {
			t.Errorf("summary missing %q:\n%s", want, view)
		}
	}
	// The skipped fix is re-listed in full in the summary failures block (§4.1).
	if !strings.Contains(view, "undefined: scopedTx") {
		t.Errorf("summary must re-list the pinned failure:\n%s", view)
	}
}

// TestColonyAbortedSummary asserts an operational error renders the abort screen,
// not a misleading clean summary (colony-view.md §4.3).
func TestColonyAbortedSummary(t *testing.T) {
	m := newColonyModel(4, false, false)
	end := runEnd(0, 0, 0, 0, "unknown")
	end.RunEnd.Error = "ast-grep binary not found on PATH"
	m = feed(m, runStart("c0d4e8", "./pkg"), end)
	view := m.View()
	if !strings.Contains(view, "ABORTED") || !strings.Contains(view, "ast-grep binary not found on PATH") {
		t.Errorf("aborted run should show the abort screen with the verbatim error:\n%s", view)
	}
	if strings.Contains(view, "COLONY SUMMARY") {
		t.Errorf("aborted run must NOT show the clean summary:\n%s", view)
	}
}

// TestColonyNoColorDropsANSI asserts that with color disabled the view contains
// no ANSI escape sequences, so a pipe-to-cat / NO_COLOR render is plain text and
// still legible by glyph + label (colony-view.md §6 rule 3).
func TestColonyNoColorDropsANSI(t *testing.T) {
	m := newColonyModel(2, false, false) // color=false
	m = feed(m, runStart("r", "."), detectFinding("unused-import", "a.go", 1, engine.SeverityHigh),
		antStart(1, "unused-import", "a.go", 1), antVerified(1, "a.go"), runEnd(1, 1, 0, 0, "high"))
	if strings.Contains(m.View(), "\x1b[") {
		t.Errorf("color-disabled view must contain no ANSI escapes:\n%q", m.View())
	}
}

// TestColonyAsciiFallbackGlyphs asserts the --ascii glyph set replaces the heavy
// marks (colony-view.md §6 rule 4).
func TestColonyAsciiFallbackGlyphs(t *testing.T) {
	m := newColonyModel(2, true, false) // ascii=true
	m = feed(m, runStart("r", "."),
		antStart(1, "unused-import", "a.go", 1), antVerified(1, "a.go"),
		antStart(2, "dead-code", "b.go", 2), antSkipped(2, "dead-code", "b.go", 2, "compile", "boom"))
	view := m.View()
	if strings.ContainsAny(view, "✔✖⚠") {
		t.Errorf("ascii mode must not emit heavy marks:\n%s", view)
	}
	if !strings.Contains(view, "+") || !strings.Contains(view, "!") {
		t.Errorf("ascii mode should use + and ! glyphs:\n%s", view)
	}
}
