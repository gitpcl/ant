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
func renderEvents(t *testing.T, opts HumanOptions, evs ...Event) string {
	t.Helper()
	bus := NewBus()
	sub := bus.Subscribe()
	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- RenderHuman(&buf, sub, opts) }()
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
	out := renderEvents(t, HumanOptions{},
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
	out := renderEvents(t, HumanOptions{All: true, Detail: true},
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
	out := renderEvents(t, HumanOptions{All: true, Detail: true},
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
	out := renderEvents(t, HumanOptions{},
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
	out := renderEvents(t, HumanOptions{},
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
	out := renderEvents(t, HumanOptions{},
		Event{Type: TypeRunStart, RunStart: &RunStartPayload{RunID: "r", Scope: engine.Scope{Root: "."}}},
		Event{Type: TypeRunEnd, RunEnd: &RunEndPayload{RunID: "r", HighestSeverity: "unknown", Error: "ast-grep unavailable"}},
	)
	if strings.Contains(out, "Nothing was modified.") || strings.Contains(out, "No findings.") {
		t.Errorf("aborted run must NOT print a clean-scan summary:\n%s", out)
	}
}

// finding is a small constructor for the digest tests.
func finding(species, file string, line int, sev engine.Severity) engine.Finding {
	return engine.Finding{
		Species:  species,
		File:     file,
		Span:     engine.Span{StartLine: line, StartCol: 1},
		Severity: sev,
		Message:  species + " message",
	}
}

func findingEvent(f engine.Finding) Event {
	return Event{Type: TypeDetectFinding, DetectFinding: &DetectFindingPayload{RunID: "r", Finding: f}}
}

// digestEvents builds a run.start → findings → run.end stream and renders it with
// the DEFAULT (digest) human options.
func digestEvents(t *testing.T, highest string, fs ...engine.Finding) string {
	t.Helper()
	evs := []Event{{Type: TypeRunStart, RunStart: &RunStartPayload{RunID: "r", Scope: engine.Scope{Root: "."}}}}
	for _, f := range fs {
		evs = append(evs, findingEvent(f))
	}
	evs = append(evs, Event{Type: TypeRunEnd, RunEnd: &RunEndPayload{RunID: "r", Findings: len(fs), HighestSeverity: highest}})
	return renderEvents(t, HumanOptions{}, evs...)
}

// TestDigestHeaderShowsScannedRoot: the digest header reports the ACTUAL scanned
// root captured from run.start (a subtree scan must not misreport "."). The root
// lives only in the human renderer's buffered state — never on RunEndPayload — so
// the --json contract is untouched.
func TestDigestHeaderShowsScannedRoot(t *testing.T) {
	out := renderEvents(t, HumanOptions{},
		Event{Type: TypeRunStart, RunStart: &RunStartPayload{RunID: "r", Scope: engine.Scope{Root: "./foo/bar"}}},
		findingEvent(finding("long-function", "foo/bar/x.go", 1, engine.SeverityMedium)),
		Event{Type: TypeRunEnd, RunEnd: &RunEndPayload{RunID: "r", Findings: 1, HighestSeverity: "medium"}},
	)
	if !strings.Contains(out, "ant scout · scanned ./foo/bar · 1 findings") {
		t.Errorf("digest header must show the scanned root ./foo/bar:\n%s", out)
	}
	if strings.Contains(out, "scanned . ·") {
		t.Errorf("digest header must NOT fall back to '.' when a root was given:\n%s", out)
	}
}

// TestDigestSeverityBreakdown: the default digest header shows the per-severity
// breakdown with counts, omitting any tier with zero findings.
func TestDigestSeverityBreakdown(t *testing.T) {
	out := digestEvents(t, "high",
		finding("hardcoded-secret", "a.go", 1, engine.SeverityHigh),
		finding("long-function", "b.go", 2, engine.SeverityMedium),
		finding("long-function", "c.go", 3, engine.SeverityMedium),
		finding("magic-number", "d.go", 4, engine.SeverityLow),
	)
	for _, want := range []string{
		"ant scout · scanned . · 4 findings",
		"high     1",
		"medium   2",
		"low      1",
		"Nothing was modified.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("digest missing %q:\n%s", want, out)
		}
	}
}

// TestDigestOmitsZeroTier: a tier with no findings is not printed.
func TestDigestOmitsZeroTier(t *testing.T) {
	out := digestEvents(t, "medium",
		finding("long-function", "b.go", 2, engine.SeverityMedium),
	)
	if strings.Contains(out, "high ") || strings.Contains(out, "\nHIGH") {
		t.Errorf("digest must omit the empty high tier:\n%s", out)
	}
	if strings.Contains(out, "low ") {
		t.Errorf("digest must omit the empty low tier:\n%s", out)
	}
}

// TestDigestListsAllHighInFull: every high finding appears with file:line +
// species, sorted by file path. Medium/low never appear as individual lines.
func TestDigestListsAllHighInFull(t *testing.T) {
	out := digestEvents(t, "high",
		finding("vue-v-html-xss", "z/Comment.vue", 6, engine.SeverityHigh),
		finding("hardcoded-secret", "a/config.go", 24, engine.SeverityHigh),
		finding("long-function", "m.go", 2, engine.SeverityMedium),
	)
	for _, want := range []string{
		"HIGH (2)",
		"a/config.go:24",
		"hardcoded-secret",
		"z/Comment.vue:6",
		"vue-v-html-xss",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("digest HIGH block missing %q:\n%s", want, out)
		}
	}
	// High block sorted by path: a/config.go precedes z/Comment.vue.
	if strings.Index(out, "a/config.go") > strings.Index(out, "z/Comment.vue") {
		t.Errorf("HIGH block not sorted by file path:\n%s", out)
	}
	// The medium finding must NOT appear as an individual file:line line.
	if strings.Contains(out, "m.go:2") {
		t.Errorf("digest must not list medium findings individually:\n%s", out)
	}
}

// TestDigestFoldsMediumLowToSpeciesCounts: medium/low are summarized as per-
// species counts sorted by count desc (tie-break species asc), capped with a
// "+ K more species" remainder.
func TestDigestFoldsMediumLowToSpeciesCounts(t *testing.T) {
	var fs []engine.Finding
	// long-function x3, magic-number x2, then 8 distinct 1-count species so the
	// total exceeds the maxDigestSpecies cap and forces a "+ K more" line.
	fs = append(fs, finding("long-function", "a.go", 1, engine.SeverityMedium))
	fs = append(fs, finding("long-function", "b.go", 1, engine.SeverityMedium))
	fs = append(fs, finding("long-function", "c.go", 1, engine.SeverityLow))
	fs = append(fs, finding("magic-number", "d.go", 1, engine.SeverityMedium))
	fs = append(fs, finding("magic-number", "e.go", 1, engine.SeverityMedium))
	for _, s := range []string{"aaa", "bbb", "ccc", "ddd", "eee", "fff", "ggg", "hhh"} {
		fs = append(fs, finding(s, s+".go", 1, engine.SeverityLow))
	}
	out := digestEvents(t, "medium", fs...)

	if !strings.Contains(out, "TOP SPECIES (medium / low)") {
		t.Errorf("digest missing species block header:\n%s", out)
	}
	// Highest count first.
	li := strings.Index(out, "long-function")
	mi := strings.Index(out, "magic-number")
	if li < 0 || mi < 0 || li > mi {
		t.Errorf("species not sorted by count desc (long-function before magic-number):\n%s", out)
	}
	// 10 distinct species (long-function, magic-number + 8), cap 8 → "+ 2 more".
	if !strings.Contains(out, "+ 2 more species") {
		t.Errorf("digest missing the '+ K more species' remainder:\n%s", out)
	}
	// The --all hint appears because medium/low were folded away.
	if !strings.Contains(out, "→ ant scout --all") {
		t.Errorf("digest missing the --all hint:\n%s", out)
	}
}

// TestAllFlatListPreservesEveryLine: --all restores the full one-per-line flat
// list (every finding present), sorted high→low, and --detail still composes.
func TestAllFlatListPreservesEveryLine(t *testing.T) {
	fs := []engine.Finding{
		finding("long-function", "a.go", 10, engine.SeverityMedium),
		finding("hardcoded-secret", "b.go", 20, engine.SeverityHigh),
		finding("magic-number", "c.go", 30, engine.SeverityLow),
	}
	evs := []Event{{Type: TypeRunStart, RunStart: &RunStartPayload{RunID: "r", Scope: engine.Scope{Root: "."}}}}
	for _, f := range fs {
		evs = append(evs, findingEvent(f))
	}
	evs = append(evs, Event{Type: TypeRunEnd, RunEnd: &RunEndPayload{RunID: "r", Findings: 3, HighestSeverity: "high"}})
	out := renderEvents(t, HumanOptions{All: true}, evs...)

	for _, want := range []string{"a.go:10:1", "b.go:20:1", "c.go:30:1", "Nothing was modified."} {
		if !strings.Contains(out, want) {
			t.Errorf("--all flat list missing %q:\n%s", want, out)
		}
	}
	// Sorted high→low: the high finding (b.go) precedes the medium (a.go).
	if strings.Index(out, "b.go:20") > strings.Index(out, "a.go:10") {
		t.Errorf("--all flat list not sorted high→low severity:\n%s", out)
	}
	// The digest header must NOT appear in --all mode.
	if strings.Contains(out, "TOP SPECIES") || strings.Contains(out, "scanned . ·") {
		t.Errorf("--all must not render the digest:\n%s", out)
	}
}

// TestDigestSanitizesControlChars: the Sprint-020 terminal-escape-injection
// defense applies to the NEW digest surface — a high finding whose Species/File
// carry an embedded ESC must not leak the control byte into the digest.
func TestDigestSanitizesControlChars(t *testing.T) {
	evil := finding("evil\x1b[2J-species", "we\x1bird.go", 9, engine.SeverityHigh)
	out := digestEvents(t, "high", evil)
	if strings.IndexByte(out, 0x1b) >= 0 {
		t.Errorf("digest leaked an ESC control byte (escape injection not stripped):\n%q", out)
	}
	if !strings.Contains(out, "evil[2J-species") {
		t.Errorf("sanitized species text not preserved in digest:\n%q", out)
	}
}
