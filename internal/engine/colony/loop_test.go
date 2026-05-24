package colony_test

import (
	"context"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/colony"
	"github.com/gitpcl/ant/internal/engine/events"
	local "github.com/gitpcl/ant/internal/engine/store"
)

// newRun builds a local Store with runID saved and returns it ready for staging.
func newRun(t *testing.T, runID string) *local.Store {
	t.Helper()
	st := local.New(t.TempDir())
	if err := st.SaveRun(engine.Run{ID: runID, StartedAt: "2026-05-24T00:00:00Z"}); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	return st
}

func finding(file string, line int, sev engine.Severity) engine.Finding {
	return engine.Finding{
		Species:  "unused-import",
		File:     file,
		Span:     engine.Span{StartLine: line, EndLine: line},
		Severity: sev,
		Message:  "unused import",
		Snippet:  "import \"os\"",
	}
}

// TestSpikeOneFindingOneStagedDiff is the approach-gate SPIKE: run the loop
// SERIALLY (concurrency 1) with a fake Fixer + fake passing Verifier and prove
// one finding → exactly one staged diff + one ant.verified event, within
// run.start…run.end. This validates the loop shape before concurrency is added.
func TestSpikeOneFindingOneStagedDiff(t *testing.T) {
	st := newRun(t, "spike-1")
	bus := events.NewBus()
	sub := bus.Subscribe()
	evDone := collect(sub)

	fixer := &fakeFixer{}
	ants := []colony.Ant{{
		Finding:  finding("a.go", 3, engine.SeverityLow),
		Fixer:    fixer,
		Verifier: fakePassVerifier{},
	}}

	res, err := colony.Run(context.Background(), bus, colony.Options{
		Scope:       engine.Scope{Root: "."},
		Ants:        ants,
		Store:       st,
		RunID:       "spike-1",
		Concurrency: 1,
	})
	bus.Close()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Exactly one diff staged in the Store.
	staged, err := st.ListStaged("spike-1")
	if err != nil {
		t.Fatalf("ListStaged: %v", err)
	}
	if len(staged) != 1 {
		t.Fatalf("staged diffs: got %d, want 1", len(staged))
	}
	if staged[0].Fixer != "fake-fixer" {
		t.Errorf("staged provenance: got %q, want fake-fixer", staged[0].Fixer)
	}

	// Result counts.
	if res.Verified != 1 || res.Skipped != 0 || res.Staged != 1 {
		t.Errorf("result = %+v; want Verified 1, Skipped 0, Staged 1", res)
	}
	if fixer.calls.Load() != 1 {
		t.Errorf("fixer called %d times, want 1", fixer.calls.Load())
	}

	// Event sequence: run.start, ant.start, ant.verified, run.end, no skips.
	evs := <-evDone
	if _, ok := firstOf(evs, events.TypeRunStart); !ok {
		t.Error("missing run.start")
	}
	if countType(evs, events.TypeAntStart) != 1 {
		t.Errorf("ant.start count = %d, want 1", countType(evs, events.TypeAntStart))
	}
	if countType(evs, events.TypeAntVerified) != 1 {
		t.Errorf("ant.verified count = %d, want 1", countType(evs, events.TypeAntVerified))
	}
	if countType(evs, events.TypeAntSkipped) != 0 {
		t.Errorf("ant.skipped count = %d, want 0", countType(evs, events.TypeAntSkipped))
	}
	if _, ok := firstOf(evs, events.TypeRunEnd); !ok {
		t.Error("missing run.end")
	}
	// run.start must be first and run.end last (well-formed stream).
	if evs[0].Type != events.TypeRunStart {
		t.Errorf("first event = %s, want run.start", evs[0].Type)
	}
	if evs[len(evs)-1].Type != events.TypeRunEnd {
		t.Errorf("last event = %s, want run.end", evs[len(evs)-1].Type)
	}
}

// TestFailingVerifierSkips proves a failing verifier discards the diff (nothing
// staged) and emits ant.skipped carrying the failing check (PRD §6.3 — a skip is
// visible, never silent).
func TestFailingVerifierSkips(t *testing.T) {
	st := newRun(t, "skip-1")
	bus := events.NewBus()
	sub := bus.Subscribe()
	evDone := collect(sub)

	ants := []colony.Ant{{
		Finding:  finding("a.go", 3, engine.SeverityHigh),
		Fixer:    &fakeFixer{},
		Verifier: fakeFailVerifier{check: "compile"},
	}}

	res, err := colony.Run(context.Background(), bus, colony.Options{
		Scope:       engine.Scope{Root: "."},
		Ants:        ants,
		Store:       st,
		RunID:       "skip-1",
		Concurrency: 1,
	})
	bus.Close()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	staged, _ := st.ListStaged("skip-1")
	if len(staged) != 0 {
		t.Errorf("staged diffs: got %d, want 0 (failing verifier must discard)", len(staged))
	}
	if res.Verified != 0 || res.Skipped != 1 {
		t.Errorf("result = %+v; want Verified 0, Skipped 1", res)
	}

	evs := <-evDone
	if countType(evs, events.TypeAntVerified) != 0 {
		t.Error("a failing verifier must not emit ant.verified")
	}
	skipEv, ok := firstOf(evs, events.TypeAntSkipped)
	if !ok {
		t.Fatal("missing ant.skipped for a failed verifier")
	}
	if skipEv.AntSkipped.FailedCheck.Name != "compile" {
		t.Errorf("skipped FailedCheck.Name = %q, want compile", skipEv.AntSkipped.FailedCheck.Name)
	}
	if skipEv.AntSkipped.FailedCheck.Passed {
		t.Error("FailedCheck.Passed should be false")
	}
	if skipEv.AntSkipped.Reason == "" {
		t.Error("ant.skipped must carry a reason (the failing detail)")
	}
}

// TestFixerErrorSkips proves a fixer that errors becomes a skip (never a silent
// drop, never a run abort), surfaced with a "fix" check.
func TestFixerErrorSkips(t *testing.T) {
	st := newRun(t, "fixerr-1")
	bus := events.NewBus()
	sub := bus.Subscribe()
	evDone := collect(sub)

	res, err := colony.Run(context.Background(), bus, colony.Options{
		Ants: []colony.Ant{{
			Finding:  finding("a.go", 1, engine.SeverityLow),
			Fixer:    fakeErrFixer{},
			Verifier: fakePassVerifier{},
		}},
		Store:       st,
		RunID:       "fixerr-1",
		Concurrency: 1,
	})
	bus.Close()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Skipped != 1 || res.Verified != 0 {
		t.Errorf("result = %+v; want Skipped 1, Verified 0", res)
	}
	evs := <-evDone
	skipEv, ok := firstOf(evs, events.TypeAntSkipped)
	if !ok {
		t.Fatal("a fixer error must emit ant.skipped")
	}
	if skipEv.AntSkipped.FailedCheck.Name != "fix" {
		t.Errorf("FailedCheck.Name = %q, want fix", skipEv.AntSkipped.FailedCheck.Name)
	}
}

// TestMixedRunStagesOnlyVerified runs a mix of passing and failing ants serially
// and asserts only the verified ones are staged and the counts add up.
func TestMixedRunStagesOnlyVerified(t *testing.T) {
	st := newRun(t, "mixed-1")
	bus := events.NewBus()
	sub := bus.Subscribe()
	evDone := collect(sub)

	ants := []colony.Ant{
		{Finding: finding("pass1.go", 1, engine.SeverityLow), Fixer: &fakeFixer{}, Verifier: fakePassVerifier{}},
		{Finding: finding("fail1.go", 1, engine.SeverityHigh), Fixer: &fakeFixer{}, Verifier: fakeFailVerifier{}},
		{Finding: finding("pass2.go", 1, engine.SeverityMedium), Fixer: &fakeFixer{}, Verifier: fakePassVerifier{}},
	}

	res, err := colony.Run(context.Background(), bus, colony.Options{
		Ants: ants, Store: st, RunID: "mixed-1", Concurrency: 1,
	})
	bus.Close()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Verified != 2 || res.Skipped != 1 || res.Staged != 2 {
		t.Errorf("result = %+v; want Verified 2, Skipped 1, Staged 2", res)
	}
	staged, _ := st.ListStaged("mixed-1")
	if len(staged) != 2 {
		t.Errorf("staged = %d, want 2", len(staged))
	}

	evs := <-evDone
	// run.end carries the highest severity seen (high, from the failing ant).
	end, _ := firstOf(evs, events.TypeRunEnd)
	if end.RunEnd.HighestSeverity != "high" {
		t.Errorf("run.end HighestSeverity = %q, want high", end.RunEnd.HighestSeverity)
	}
	if end.RunEnd.Verified != 2 || end.RunEnd.Skipped != 1 {
		t.Errorf("run.end counts = verified %d skipped %d; want 2,1", end.RunEnd.Verified, end.RunEnd.Skipped)
	}
}
