package colony

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/events"
	"github.com/gitpcl/ant/internal/engine/stage"
	local "github.com/gitpcl/ant/internal/engine/store"
)

// fakeDetector reports a fixed set of findings for a species (no live ast-grep).
type fakeDetector struct{ findings []engine.Finding }

func (d fakeDetector) Detect(context.Context, engine.Scope) ([]engine.Finding, error) {
	return d.findings, nil
}

// fakeFixer returns a one-line delete diff with provenance set to the species.
type fakeFixer struct{ fixer string }

func (f fakeFixer) Fix(_ context.Context, task engine.FixTask) (engine.ProposedDiff, error) {
	return engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: task.Finding.File, Patch: "@@ -1,1 +1,0 @@\n-x\n"}},
		Fixer: f.fixer,
	}, nil
}

// passVerifier always passes. recordedApplier records what it was asked to land.
type passVerifier struct{}

func (passVerifier) Verify(context.Context, engine.ProposedDiff, engine.Scope) engine.VerifyResult {
	return engine.VerifyResult{Passed: true, Checks: []engine.CheckResult{{Name: "compile", Passed: true}}}
}

type recordedApplier struct{ landed []engine.StagedRecord }

func (a *recordedApplier) ApplyRecords(_ context.Context, bus *events.Bus, runID string, records []engine.StagedRecord) (int, error) {
	a.landed = append(a.landed, records...)
	for _, rec := range records {
		bus.Publish(events.Event{Type: events.TypeApplyDone, ApplyDone: &events.ApplyDonePayload{
			RunID: runID, Path: rec.Diff.Files[0].Path, Branch: "ant/fix-" + runID, Commit: "deadbeef"}})
	}
	return len(records), nil
}

func newDriveOpts(t *testing.T, recipes map[string]SpeciesRecipe, detectors []engine.NamedDetector) DriveOptions {
	t.Helper()
	return DriveOptions{
		Scope:       engine.Scope{Root: t.TempDir()},
		Detectors:   detectors,
		Recipes:     recipes,
		Store:       local.New(t.TempDir()),
		RunID:       "fixrun",
		Concurrency: 2,
		Now:         func() time.Time { return time.Unix(0, 0).UTC() },
		Renderer:    RendererJSON, // tests render to a buffer, not a TTY
	}
}

// TestFixStagesVerifiedDiffsNothingApplied is the headline `ant fix` criterion:
// verified diffs land in STAGING and nothing is applied without --apply.
func TestFixStagesVerifiedDiffsNothingApplied(t *testing.T) {
	det := []engine.NamedDetector{{Species: "unused-import", Detector: fakeDetector{findings: []engine.Finding{
		{Species: "unused-import", File: "a.go", Span: engine.Span{StartLine: 1}, Severity: engine.SeverityHigh},
		{Species: "unused-import", File: "b.go", Span: engine.Span{StartLine: 2}, Severity: engine.SeverityLow},
	}}}}
	recipes := map[string]SpeciesRecipe{"unused-import": {
		Fixer:       fakeFixer{fixer: "deterministic (delete-match)"},
		NewVerifier: func(engine.Finding) engine.Verifier { return passVerifier{} },
		AutoApply:   true,
	}}
	opts := newDriveOpts(t, recipes, det)

	var buf bytes.Buffer
	res, err := Drive(context.Background(), &buf, opts)
	if err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if res.Verified != 2 || res.Staged != 2 {
		t.Errorf("expected 2 verified+staged, got verified=%d staged=%d", res.Verified, res.Staged)
	}

	// The diffs are in the Store's staged records (NOT applied — no Applier set).
	records, err := stage.New(opts.Store, "fixrun").ListRecords()
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 staged records, got %d", len(records))
	}
	for _, rec := range records {
		if rec.Mark != engine.MarkPending {
			t.Errorf("staged record should be pending (not applied), got %v", rec.Mark)
		}
		if rec.Finding.Species != "unused-import" || rec.Verify.Passed != true {
			t.Errorf("staged record missing provenance/verify: %+v", rec)
		}
	}

	// --json stream is well-formed: starts run.start, ends run.end, no apply.done.
	out := buf.String()
	if !strings.Contains(out, `"type":"run.start"`) || !strings.Contains(out, `"type":"run.end"`) {
		t.Errorf("json stream not well-formed:\n%s", out)
	}
	if strings.Contains(out, `"type":"apply.done"`) {
		t.Errorf("nothing should be applied without --apply, but saw apply.done:\n%s", out)
	}
}

// TestFixApplyOnlyTrustedSpecies proves --apply auto-lands diffs from a trusted
// species (auto_apply true) while an untrusted species (auto_apply false) stays
// staged in the SAME run (ADR-0002 per-species trust).
func TestFixApplyOnlyTrustedSpecies(t *testing.T) {
	det := []engine.NamedDetector{
		{Species: "unused-import", Detector: fakeDetector{findings: []engine.Finding{
			{Species: "unused-import", File: "trusted.go", Span: engine.Span{StartLine: 1}, Severity: engine.SeverityHigh}}}},
		{Species: "n+1-query", Detector: fakeDetector{findings: []engine.Finding{
			{Species: "n+1-query", File: "untrusted.go", Span: engine.Span{StartLine: 1}, Severity: engine.SeverityHigh}}}},
	}
	recipes := map[string]SpeciesRecipe{
		"unused-import": {Fixer: fakeFixer{fixer: "deterministic (delete-match)"}, NewVerifier: func(engine.Finding) engine.Verifier { return passVerifier{} }, AutoApply: true},
		"n+1-query":     {Fixer: fakeFixer{fixer: "rawmodel (qwen)"}, NewVerifier: func(engine.Finding) engine.Verifier { return passVerifier{} }, AutoApply: false},
	}
	opts := newDriveOpts(t, recipes, det)
	applier := &recordedApplier{}
	opts.Apply = applier
	opts.ApplyFused = true

	var buf bytes.Buffer
	if _, err := Drive(context.Background(), &buf, opts); err != nil {
		t.Fatalf("Drive: %v", err)
	}

	// Exactly the trusted species' diff was handed to the applier.
	if len(applier.landed) != 1 {
		t.Fatalf("expected exactly 1 trusted diff applied, got %d", len(applier.landed))
	}
	if applier.landed[0].Finding.Species != "unused-import" {
		t.Errorf("the wrong species was auto-applied: %q (only auto_apply=true should land)", applier.landed[0].Finding.Species)
	}
	// run.end reports Applied=1.
	if !strings.Contains(buf.String(), `"applied":1`) {
		t.Errorf("run.end should report applied=1:\n%s", buf.String())
	}
}

// TestFixMissingRecipeIsVisibleSkip proves a finding whose species has no recipe
// is surfaced as a skip (ant.skipped), never silently dropped (PRD §6.3).
func TestFixMissingRecipeIsVisibleSkip(t *testing.T) {
	det := []engine.NamedDetector{{Species: "ghost", Detector: fakeDetector{findings: []engine.Finding{
		{Species: "ghost", File: "x.go", Span: engine.Span{StartLine: 1}, Severity: engine.SeverityMedium}}}}}
	opts := newDriveOpts(t, map[string]SpeciesRecipe{}, det) // no recipe for "ghost"

	var buf bytes.Buffer
	res, err := Drive(context.Background(), &buf, opts)
	if err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if res.Skipped != 1 || res.Verified != 0 {
		t.Errorf("a recipe-less finding should skip: verified=%d skipped=%d", res.Verified, res.Skipped)
	}
	if !strings.Contains(buf.String(), `"type":"ant.skipped"`) {
		t.Errorf("missing-recipe skip must be visible in the event stream:\n%s", buf.String())
	}
}

// TestFixDetectorErrorIsOperational proves a detector error aborts the run with
// an operational error (exit 2) and a well-formed run.end carrying the error.
func TestFixDetectorErrorIsOperational(t *testing.T) {
	det := []engine.NamedDetector{{Species: "x", Detector: errDetector{}}}
	opts := newDriveOpts(t, map[string]SpeciesRecipe{}, det)

	var buf bytes.Buffer
	_, err := Drive(context.Background(), &buf, opts)
	if err == nil {
		t.Fatal("a detector error should abort the run")
	}
	if !strings.Contains(buf.String(), `"type":"run.end"`) || !strings.Contains(buf.String(), `"error":`) {
		t.Errorf("aborted run should still emit run.end with the error:\n%s", buf.String())
	}
}

type errDetector struct{}

func (errDetector) Detect(context.Context, engine.Scope) ([]engine.Finding, error) {
	return nil, &engine.DetectorUnavailableError{Detector: "ast-grep", Binary: "ast-grep"}
}
