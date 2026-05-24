package events

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
)

// renderEvents feeds a scripted event sequence through RenderHuman and returns
// the rendered text. It exercises the same renderer the CLI attaches to a scout
// run.
func renderEvents(t *testing.T, detail bool, evs ...Event) string {
	t.Helper()
	bus := NewBus()
	sub := bus.Subscribe()
	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- RenderHuman(&buf, sub, detail) }()
	for _, ev := range evs {
		bus.Publish(ev)
	}
	bus.Close()
	if err := <-done; err != nil {
		t.Fatalf("RenderHuman: %v", err)
	}
	return buf.String()
}

func TestRenderHumanStatesNothingModified(t *testing.T) {
	out := renderEvents(t, false,
		Event{Type: TypeRunStart, RunStart: &RunStartPayload{RunID: "r", Scope: engine.Scope{Root: "."}}},
		Event{Type: TypeRunEnd, RunEnd: &RunEndPayload{RunID: "r", Findings: 0, HighestSeverity: "unknown"}},
	)
	// PRD §6.1 / ADR 0001: scout (and bare ant) must clearly state nothing was
	// modified, even on a clean zero-finding run.
	if !strings.Contains(out, "Nothing was modified.") {
		t.Errorf("output missing the 'Nothing was modified' line:\n%s", out)
	}
	if !strings.Contains(out, "No findings.") {
		t.Errorf("clean run should say 'No findings':\n%s", out)
	}
}

func TestRenderHumanListsFindings(t *testing.T) {
	f := engine.Finding{
		Species:  "unused-import",
		File:     "main.go",
		Span:     engine.Span{StartLine: 3, StartCol: 1},
		Severity: engine.SeverityHigh,
		Message:  "Imported package is never used",
		Snippet:  "import \"strings\"",
	}
	out := renderEvents(t, true,
		Event{Type: TypeRunStart, RunStart: &RunStartPayload{RunID: "r", Scope: engine.Scope{Root: "."}}},
		Event{Type: TypeDetectFinding, DetectFinding: &DetectFindingPayload{RunID: "r", Finding: f}},
		Event{Type: TypeRunEnd, RunEnd: &RunEndPayload{RunID: "r", Findings: 1, HighestSeverity: "high"}},
	)
	for _, want := range []string{"main.go:3:1", "high", "unused-import", "Imported package is never used", "Nothing was modified."} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// --detail includes the snippet.
	if !strings.Contains(out, "import \"strings\"") {
		t.Errorf("--detail output missing the snippet:\n%s", out)
	}
}

func TestRenderHumanAbortedRunDoesNotClaimClean(t *testing.T) {
	out := renderEvents(t, false,
		Event{Type: TypeRunStart, RunStart: &RunStartPayload{RunID: "r", Scope: engine.Scope{Root: "."}}},
		Event{Type: TypeRunEnd, RunEnd: &RunEndPayload{RunID: "r", HighestSeverity: "unknown", Error: "ast-grep unavailable"}},
	)
	if strings.Contains(out, "Nothing was modified.") || strings.Contains(out, "No findings.") {
		t.Errorf("aborted run must NOT print a clean-scan summary:\n%s", out)
	}
}
