package scout

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/detect"
	"github.com/gitpcl/ant/internal/engine/events"
)

// fakeDetector returns canned findings (or an error) so scout's orchestration,
// filtering, and event sequence are tested without any external binary. It stands
// in for a built-in ast-grep detector, so it is ScanSafe (runs no script) — scout
// admits only scan-safe detectors (the Sprint-020 defense-in-depth guard).
type fakeDetector struct {
	findings []engine.Finding
	err      error
}

func (f fakeDetector) Detect(context.Context, engine.Scope) ([]engine.Finding, error) {
	return f.findings, f.err
}

// ScanSafe marks the fake as a stand-in for a vetted built-in detector (it runs
// no species-supplied script), satisfying engine.ScanSafeDetector.
func (f fakeDetector) ScanSafe() bool { return true }

// scriptDetector is a fake NON-scan-safe detector: it is a plain engine.Detector
// that does NOT implement ScanSafeDetector, standing in for a `command` detector
// that would exec a species script at scan time. Scout must REJECT it. If its
// Detect ever ran it would fail the test loudly (a scan-safe guard breach).
type scriptDetector struct{}

func (scriptDetector) Detect(context.Context, engine.Scope) ([]engine.Finding, error) {
	return nil, errors.New("scriptDetector.Detect must never be reached: scout should reject it before running")
}

func finding(species, file string, line int, sev engine.Severity) engine.Finding {
	return engine.Finding{
		Species:  species,
		File:     file,
		Span:     engine.Span{StartLine: line, StartCol: 1, EndLine: line, EndCol: 5},
		Severity: sev,
		Message:  species + " issue",
		Snippet:  "code",
	}
}

func recordedFixture(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "..", "testdata", "astgrep-output.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recorded ast-grep fixture: %v", err)
	}
	return data
}

// drain subscribes, runs scout, and returns all events plus the result. Used by
// the orchestration tests.
func drain(t *testing.T, opts Options) ([]events.Event, Result, error) {
	t.Helper()
	bus := events.NewBus()
	sub := bus.Subscribe()

	got := make(chan []events.Event, 1)
	go func() {
		var evs []events.Event
		for ev := range sub.C {
			evs = append(evs, ev)
		}
		got <- evs
	}()

	res, err := Run(context.Background(), bus, opts)
	bus.Close()
	return <-got, res, err
}

func TestScoutReportsFindingsAndEventSequence(t *testing.T) {
	opts := Options{
		Scope: engine.Scope{Root: "."},
		Detectors: []engine.NamedDetector{
			{Species: "unused-import", Detector: detect.NewRecorded("unused-import", recordedFixture(t))},
		},
		RunID: "fixed",
	}
	evs, res, err := drain(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Findings) != 2 {
		t.Fatalf("got %d findings, want 2", len(res.Findings))
	}
	if res.HighestSeverity != engine.SeverityHigh {
		t.Errorf("highest severity = %v, want high", res.HighestSeverity)
	}

	// Event order: run.start, then a detect.finding per finding, then run.end.
	if len(evs) != 4 {
		t.Fatalf("got %d events, want 4 (run.start + 2 findings + run.end)", len(evs))
	}
	if evs[0].Type != events.TypeRunStart {
		t.Errorf("first event = %s, want run.start", evs[0].Type)
	}
	if evs[len(evs)-1].Type != events.TypeRunEnd {
		t.Errorf("last event = %s, want run.end", evs[len(evs)-1].Type)
	}
	if evs[1].Type != events.TypeDetectFinding || evs[2].Type != events.TypeDetectFinding {
		t.Errorf("middle events = %s,%s, want two detect.finding", evs[1].Type, evs[2].Type)
	}
	if evs[len(evs)-1].RunEnd.Error != "" {
		t.Errorf("clean run reported error %q", evs[len(evs)-1].RunEnd.Error)
	}
}

func TestScoutAntFilterLimitsSpecies(t *testing.T) {
	opts := Options{
		Scope: engine.Scope{Root: "."},
		Detectors: []engine.NamedDetector{
			{Species: "unused-import", Detector: fakeDetector{findings: []engine.Finding{finding("unused-import", "a.go", 1, engine.SeverityHigh)}}},
			{Species: "dead-code", Detector: fakeDetector{findings: []engine.Finding{finding("dead-code", "b.go", 2, engine.SeverityMedium)}}},
		},
		AntFilter: []string{"unused-import"},
		RunID:     "fixed",
	}
	_, res, err := drain(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Species != "unused-import" {
		t.Fatalf("--ant filter failed: got %+v", res.Findings)
	}
}

func TestScoutSeverityFilterDropsLowFindings(t *testing.T) {
	opts := Options{
		Scope: engine.Scope{Root: "."},
		Detectors: []engine.NamedDetector{
			{Species: "x", Detector: fakeDetector{findings: []engine.Finding{
				finding("x", "a.go", 1, engine.SeverityLow),
				finding("x", "b.go", 2, engine.SeverityHigh),
			}}},
		},
		SeverityFilter: engine.SeverityMedium,
		RunID:          "fixed",
	}
	_, res, err := drain(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Severity != engine.SeverityHigh {
		t.Fatalf("severity filter failed: got %+v", res.Findings)
	}
}

func TestScoutDetectorErrorAbortsWithRunEnd(t *testing.T) {
	wantErr := &engine.DetectorUnavailableError{Detector: "ast-grep", Binary: "ast-grep", Err: errors.New("not found")}
	opts := Options{
		Scope: engine.Scope{Root: "."},
		Detectors: []engine.NamedDetector{
			{Species: "x", Detector: fakeDetector{err: wantErr}},
		},
		RunID: "fixed",
	}
	evs, _, err := drain(t, opts)
	if !errors.Is(err, engine.ErrOperational) {
		t.Fatalf("error = %v, want operational", err)
	}
	// Even on abort the stream is well-formed: run.start then run.end with Error.
	if len(evs) != 2 || evs[0].Type != events.TypeRunStart || evs[1].Type != events.TypeRunEnd {
		t.Fatalf("aborted stream malformed: %+v", evs)
	}
	if evs[1].RunEnd.Error == "" {
		t.Error("aborted run.end must carry the error for front doors / human renderer")
	}
}

// TestScoutNeverWritesWorkingTree is the contract-critical non-mutation proof:
// it snapshots a hash of every file in the fixture tree before and after a
// scout run and asserts byte-identical (TECHSPEC §7 — scout mutates nothing).
func TestScoutNeverWritesWorkingTree(t *testing.T) {
	// Copy the fixture into a temp dir so the assertion is hermetic.
	root := t.TempDir()
	src := filepath.Join("..", "..", "..", "testdata", "has-findings")
	copyTree(t, src, root)

	before := hashTree(t, root)

	opts := Options{
		Scope: engine.Scope{Root: root},
		Detectors: []engine.NamedDetector{
			{Species: "unused-import", Detector: detect.NewRecorded("unused-import", recordedFixture(t))},
		},
		RunID: "fixed",
	}
	if _, _, err := drain(t, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	after := hashTree(t, root)
	if len(before) != len(after) {
		t.Fatalf("file count changed: before=%d after=%d", len(before), len(after))
	}
	for path, h := range before {
		if after[path] != h {
			t.Errorf("file %s was modified by scout (before %s, after %s)", path, h, after[path])
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("scout created a new file: %s", path)
		}
	}
}

// TestScoutRejectsNonScanSafeDetector is the Sprint-020 defense-in-depth guard
// (FIX 1): scout must admit ONLY scan-safe detectors and reject any that would
// exec a species script at scan time (a `command` detector). A non-scan-safe
// detector is rejected with an operational error (exit code 2) NAMING the
// species, and its Detect is NEVER called — so a future change that points scout
// at resolved user/command detectors cannot silently bypass the trust gate.
func TestScoutRejectsNonScanSafeDetector(t *testing.T) {
	opts := Options{
		Scope: engine.Scope{Root: "."},
		Detectors: []engine.NamedDetector{
			// A scan-safe built-in alongside a non-scan-safe (command-like) one: the
			// presence of the unsafe one alone must abort the whole run.
			{Species: "unused-import", Detector: fakeDetector{findings: []engine.Finding{finding("unused-import", "a.go", 1, engine.SeverityHigh)}}},
			{Species: "unused-dependency", Detector: scriptDetector{}},
		},
		RunID: "fixed",
	}
	_, _, err := drain(t, opts)
	if err == nil {
		t.Fatal("scout must REJECT a non-scan-safe detector; got nil error")
	}
	if !errors.Is(err, engine.ErrOperational) {
		t.Errorf("rejection must classify as engine.ErrOperational (exit 2); got %v", err)
	}
	if !strings.Contains(err.Error(), "unused-dependency") {
		t.Errorf("error should name the offending species; got %v", err)
	}
	if !strings.Contains(err.Error(), "scan-safe") {
		t.Errorf("error should explain the scan-safe invariant; got %v", err)
	}
}

// TestScoutAdmitsScanSafeDetector is the positive companion: a run composed only
// of scan-safe detectors proceeds normally (the guard does not over-reject).
func TestScoutAdmitsScanSafeDetector(t *testing.T) {
	opts := Options{
		Scope: engine.Scope{Root: "."},
		Detectors: []engine.NamedDetector{
			{Species: "unused-import", Detector: fakeDetector{findings: []engine.Finding{finding("unused-import", "a.go", 1, engine.SeverityHigh)}}},
		},
		RunID: "fixed",
	}
	if _, _, err := drain(t, opts); err != nil {
		t.Fatalf("scout must admit a scan-safe detector; got %v", err)
	}
}

// hashTree returns a map of relative path → sha256 hex for every regular file
// under root.
func hashTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		sum := sha256.Sum256(data)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("hash tree %s: %v", root, err)
	}
	return out
}

// copyTree recursively copies src into dst (files only, preserving names).
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read fixture %s: %v", src, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyTree(t, s, d)
			continue
		}
		data, err := os.ReadFile(s)
		if err != nil {
			t.Fatalf("read %s: %v", s, err)
		}
		if err := os.WriteFile(d, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", d, err)
		}
	}
}
