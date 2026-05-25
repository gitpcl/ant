package colony

import (
	"bytes"
	"context"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/telemetry"
)

// recordingTransport captures the Report a telemetry sink sends on Close.
type recordingTransport struct{ reports []telemetry.Report }

func (r *recordingTransport) Send(rep telemetry.Report) error {
	r.reports = append(r.reports, rep)
	return nil
}

func fixedDate() string { return "2026-05-24" }

// compileFailVerifier fails on a named verifier check, so the resulting skip is
// a genuine VERIFIER CATCH (FailedCheck.Name != "fix"). Defined here because the
// shared fakeFailVerifier lives in the external colony_test package.
type compileFailVerifier struct{}

func (compileFailVerifier) Verify(context.Context, engine.ProposedDiff, engine.Scope) engine.VerifyResult {
	return engine.VerifyResult{Passed: false, Checks: []engine.CheckResult{
		{Name: "compile", Passed: false, Detail: "build broke"},
	}}
}

// driveWithTwoFindings runs a Drive with one species that VERIFIES and one that
// the verifier CATCHES (a real verifier catch), optionally attaching a telemetry
// sink. It returns the buffer so callers can assert the --json stream is intact.
func driveWithTwoFindings(t *testing.T, sink *telemetry.Sink) *bytes.Buffer {
	t.Helper()
	det := []engine.NamedDetector{
		{Species: "unused-import", Detector: fakeDetector{findings: []engine.Finding{
			{Species: "unused-import", File: "a.go", Span: engine.Span{StartLine: 1}, Severity: engine.SeverityHigh}}}},
		{Species: "n+1-query", Detector: fakeDetector{findings: []engine.Finding{
			{Species: "n+1-query", File: "b.go", Span: engine.Span{StartLine: 1}, Severity: engine.SeverityHigh}}}},
	}
	recipes := map[string]SpeciesRecipe{
		"unused-import": {Fixer: fakeFixer{fixer: "deterministic (delete-match)"}, NewVerifier: func(engine.Finding) engine.Verifier { return passVerifier{} }, AutoApply: false},
		// n+1-query proposes a diff that the verifier then catches (compile fails).
		"n+1-query": {Fixer: fakeFixer{fixer: "rawmodel (qwen)"}, NewVerifier: func(engine.Finding) engine.Verifier { return compileFailVerifier{} }, AutoApply: false},
	}
	opts := newDriveOpts(t, recipes, det)
	if sink != nil {
		opts.Telemetry = sink
	}
	var buf bytes.Buffer
	if _, err := Drive(context.Background(), &buf, opts); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	return &buf
}

// TestTelemetryDefaultOffThroughDrive proves the wiring respects the off-by-
// default contract end-to-end: with NO telemetry sink attached to DriveOptions
// (the default), a full fix run collects and sends nothing.
func TestTelemetryDefaultOffThroughDrive(t *testing.T) {
	tr := &recordingTransport{}
	// New(false,...) is the disabled state the CLI produces with no [telemetry]
	// section. Attaching it must observe nothing and send nothing.
	off := telemetry.New(false, tr, fixedDate)
	driveWithTwoFindings(t, off) // off is a nil *Sink → BusObserver no-op
	if err := off.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(tr.reports) != 0 {
		t.Fatalf("telemetry off must send nothing through Drive; got %d reports", len(tr.reports))
	}
}

// TestTelemetryObservesRealRun proves an enabled sink, attached as a plain bus
// consumer of a real Drive run, captures the right aggregates — species usage
// and the verifier catch rate (PRD §8) — WITHOUT altering the run or the --json
// stream.
func TestTelemetryObservesRealRun(t *testing.T) {
	tr := &recordingTransport{}
	sink := telemetry.New(true, tr, fixedDate)

	buf := driveWithTwoFindings(t, sink)
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(tr.reports) != 1 {
		t.Fatalf("enabled telemetry should send one report; got %d", len(tr.reports))
	}
	rep := tr.reports[0]

	// Species usage from the real detect.finding events.
	if rep.SpeciesUsage["unused-import"] != 1 || rep.SpeciesUsage["n+1-query"] != 1 {
		t.Errorf("species usage wrong: %+v", rep.SpeciesUsage)
	}
	// One verified (unused-import) + one verifier catch (n+1-query compile fail).
	if rep.VerifierCatches != 1 {
		t.Errorf("verifier catches = %d, want 1 (the n+1-query compile catch)", rep.VerifierCatches)
	}
	if rep.FixesProposed != 2 || rep.VerifierCatchRate != 0.5 {
		t.Errorf("catch rate = %v over %d proposed, want 0.5 over 2", rep.VerifierCatchRate, rep.FixesProposed)
	}

	// The --json stream is unchanged: telemetry is a passive consumer.
	out := buf.String()
	if !bytesContains(out, `"type":"run.start"`) || !bytesContains(out, `"type":"run.end"`) {
		t.Errorf("telemetry must not disturb the --json stream:\n%s", out)
	}
}

func bytesContains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}
