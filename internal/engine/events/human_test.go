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

// TestRenderHumanSanitizesControlChars is the Sprint-020 LOW fix (defense-in-
// depth): a `command` detector controls Message/Snippet, so a malicious species
// could inject ANSI/terminal-escape sequences. The human renderer must strip
// control chars (ESC, CR, NUL, BEL) before the TTY write while PRESERVING \n/\t.
// The --json path is intentionally NOT covered here (json.NewEncoder escapes
// control bytes already) — this is terminal-render only.
func TestRenderHumanSanitizesControlChars(t *testing.T) {
	// Message carries an ANSI color-reset escape + a CR + a NUL + a BEL; Snippet
	// carries an ANSI clear-screen. Tab inside the message must survive.
	f := engine.Finding{
		Species:  "unused-dependency",
		File:     "go.mod",
		Span:     engine.Span{StartLine: 5, StartCol: 1},
		Severity: engine.SeverityMedium,
		Message:  "dep \x1b[31mEVIL\x1b[0m\runimported\x00\acol\tumn",
		Snippet:  "require \x1b[2Jx v1",
	}
	out := renderEvents(t, true,
		Event{Type: TypeRunStart, RunStart: &RunStartPayload{RunID: "r", Scope: engine.Scope{Root: "."}}},
		Event{Type: TypeDetectFinding, DetectFinding: &DetectFindingPayload{RunID: "r", Finding: f}},
		Event{Type: TypeRunEnd, RunEnd: &RunEndPayload{RunID: "r", Findings: 1, HighestSeverity: "medium"}},
	)

	// No control bytes (ESC/CR/NUL/BEL) survive in the rendered output.
	for _, bad := range []struct {
		name string
		b    byte
	}{{"ESC", 0x1b}, {"CR", 0x0d}, {"NUL", 0x00}, {"BEL", 0x07}} {
		if strings.IndexByte(out, bad.b) >= 0 {
			t.Errorf("rendered output still contains a %s control byte (terminal-escape injection not stripped):\n%q", bad.name, out)
		}
	}

	// The visible text survives with control chars removed; tab is preserved.
	if !strings.Contains(out, "dep [31mEVIL[0munimportedcol\tumn") {
		t.Errorf("sanitized message text/tab not preserved as expected:\n%q", out)
	}
	if !strings.Contains(out, "require [2Jx v1") {
		t.Errorf("sanitized snippet not preserved as expected:\n%q", out)
	}
}

// TestSanitizeControl unit-tests the helper directly: control chars stripped,
// \n/\t and printable/UTF-8 preserved, and the no-control fast path returns the
// input unchanged.
func TestSanitizeControl(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain text", "plain text"},               // fast path, unchanged
		{"a\x1b[0mb", "a[0mb"},                     // ESC stripped, rest kept
		{"a\rb\x00c\ad", "abcd"},                   // CR, NUL, BEL stripped
		{"line1\nline2\tcol", "line1\nline2\tcol"}, // \n and \t preserved
		{"héllo → 世界", "héllo → 世界"},               // multi-byte UTF-8 untouched
		{"\x7fdel", "del"},                         // DEL stripped
	}
	for _, c := range cases {
		if got := sanitizeControl(c.in); got != c.want {
			t.Errorf("sanitizeControl(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRenderHumanSurfacesSkip proves a skip is visible in human/TUI output: the
// file, the failing verifier gate, and the reason all appear. A skip is a trust
// signal, never swallowed (PRD §6.3) — the human renderer must show it just as
// --json carries it.
func TestRenderHumanSurfacesSkip(t *testing.T) {
	skip := &AntSkippedPayload{
		RunID:   "r",
		AntID:   1,
		Finding: engine.Finding{File: "main.go", Span: engine.Span{StartLine: 7}, Species: "unused-import"},
		FailedCheck: engine.CheckResult{
			Name:   "compile",
			Passed: false,
			Detail: "build failed: undefined: foo",
		},
		Reason: "build failed: undefined: foo",
	}
	out := renderEvents(t, false,
		Event{Type: TypeRunStart, RunStart: &RunStartPayload{RunID: "r", Scope: engine.Scope{Root: "."}}},
		Event{Type: TypeAntSkipped, AntSkipped: skip},
		Event{Type: TypeRunEnd, RunEnd: &RunEndPayload{RunID: "r", Findings: 1, Skipped: 1, HighestSeverity: "low"}},
	)
	for _, want := range []string{"skipped", "main.go:7", "compile", "build failed: undefined: foo"} {
		if !strings.Contains(out, want) {
			t.Errorf("human output missing %q (a skip must be visible):\n%s", want, out)
		}
	}
}

// TestRenderHumanSurfacesVerified proves a verified+staged fix shows its
// provenance in human output.
func TestRenderHumanSurfacesVerified(t *testing.T) {
	v := &AntVerifiedPayload{
		RunID: "r",
		AntID: 1,
		Diff: engine.ProposedDiff{
			Files: []engine.FileDiff{{Path: "main.go", Patch: "--- a/main.go\n+++ b/main.go\n@@ -1,1 +1,0 @@\n-x\n"}},
			Fixer: "deterministic (delete-match)",
		},
	}
	out := renderEvents(t, false,
		Event{Type: TypeRunStart, RunStart: &RunStartPayload{RunID: "r", Scope: engine.Scope{Root: "."}}},
		Event{Type: TypeAntVerified, AntVerified: v},
		Event{Type: TypeRunEnd, RunEnd: &RunEndPayload{RunID: "r", Findings: 1, Verified: 1, HighestSeverity: "low"}},
	)
	for _, want := range []string{"verified", "main.go", "deterministic (delete-match)"} {
		if !strings.Contains(out, want) {
			t.Errorf("human output missing %q:\n%s", want, out)
		}
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
