package scout

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/detect"
)

// highFixture reads the recorded ast-grep payload for the has-high fixture: a
// single high-severity (1+N query) finding. Driving CI mode through this
// recorded payload exercises the real Finding mapping + severity ranking without
// a live ast-grep binary (TECHSPEC §12 — CI needs no installed matcher).
func highFixture(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "..", "testdata", "has-high", "astgrep-output.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read has-high fixture: %v", err)
	}
	return data
}

// ciExitDecision mirrors the CLI's --fail-on gate (applyFailOn in cmd/ant): the
// run exits 1 when the highest finding severity is at or above the threshold,
// else 0. Replicated here so the scout-level CI-mode test asserts the exact
// decision the gate makes from a Result, without importing the CLI package.
func ciExitDecision(highest, threshold engine.Severity) int {
	if threshold == engine.SeverityUnknown {
		return engine.ExitOK
	}
	if highest.AtLeast(threshold) {
		return engine.ExitFindings
	}
	return engine.ExitOK
}

// TestCIModeHighFindingTripsGate is the CI-mode acceptance (feature 4): a fixture
// with a high finding + --fail-on=high yields the exit-1 decision, and the same
// finding with --fail-on absent (and at a higher threshold than present) yields
// exit 0. Findings are injected through the engine via the recorded detector, so
// no live ast-grep is required.
func TestCIModeHighFindingTripsGate(t *testing.T) {
	opts := Options{
		Scope: engine.Scope{Root: filepath.Join("..", "..", "..", "testdata", "has-high")},
		Detectors: []engine.NamedDetector{
			{Species: "n+1-query", Detector: detect.NewRecorded("n+1-query", highFixture(t))},
		},
		RunID: "ci",
	}
	_, res, err := drain(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.HighestSeverity != engine.SeverityHigh {
		t.Fatalf("highest severity = %v, want high (the fixture has a high finding)", res.HighestSeverity)
	}

	// --fail-on=high → the high finding meets the threshold → exit 1.
	if got := ciExitDecision(res.HighestSeverity, engine.SeverityHigh); got != engine.ExitFindings {
		t.Errorf("--fail-on=high over a high finding: exit %d, want %d", got, engine.ExitFindings)
	}
	// No --fail-on → no gate → exit 0 regardless of findings.
	if got := ciExitDecision(res.HighestSeverity, engine.SeverityUnknown); got != engine.ExitOK {
		t.Errorf("no --fail-on: exit %d, want %d", got, engine.ExitOK)
	}
}

// TestCIModeBelowThresholdIsClean asserts a finding below the threshold does not
// trip the gate: a medium finding with --fail-on=high exits 0.
func TestCIModeBelowThresholdIsClean(t *testing.T) {
	opts := Options{
		Scope: engine.Scope{Root: "."},
		Detectors: []engine.NamedDetector{
			{Species: "dead-code", Detector: fakeDetector{findings: []engine.Finding{
				finding("dead-code", "a.go", 1, engine.SeverityMedium),
			}}},
		},
		RunID: "ci",
	}
	_, res, err := drain(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := ciExitDecision(res.HighestSeverity, engine.SeverityHigh); got != engine.ExitOK {
		t.Errorf("medium finding with --fail-on=high: exit %d, want %d (below threshold)", got, engine.ExitOK)
	}
}

// TestCIModeChangesNothing proves CI mode is read-only (feature 4: "changing
// nothing"): a scout run over the has-high fixture leaves every file
// byte-identical. Reuses the hashTree/copyTree non-mutation harness.
func TestCIModeChangesNothing(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join("..", "..", "..", "testdata", "has-high")
	copyTree(t, src, root)

	before := hashTree(t, root)
	opts := Options{
		Scope: engine.Scope{Root: root},
		Detectors: []engine.NamedDetector{
			{Species: "n+1-query", Detector: detect.NewRecorded("n+1-query", highFixture(t))},
		},
		RunID: "ci",
	}
	if _, _, err := drain(t, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	after := hashTree(t, root)
	for path, h := range before {
		if after[path] != h {
			t.Errorf("CI mode modified %s (before %s, after %s)", path, h, after[path])
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("CI mode created a new file: %s", path)
		}
	}
}
