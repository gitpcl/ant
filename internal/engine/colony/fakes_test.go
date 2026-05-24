package colony_test

import (
	"context"
	"sync/atomic"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/events"
)

// fakeFixer returns a canned ProposedDiff for any task, deriving the patch from
// the finding so distinct findings yield distinct diffs (lets a test assert
// each finding produced its own staged diff). It counts calls so a test can
// assert each finding was fixed exactly once.
type fakeFixer struct {
	calls atomic.Int64
}

func (f *fakeFixer) Fix(_ context.Context, task engine.FixTask) (engine.ProposedDiff, error) {
	f.calls.Add(1)
	return engine.ProposedDiff{
		Files: []engine.FileDiff{{
			Path:  task.Finding.File,
			Patch: "--- a/" + task.Finding.File + "\n+++ b/" + task.Finding.File + "\n@@ fake @@\n",
		}},
		Fixer:     "fake-fixer",
		Rationale: "canned fix for " + task.Finding.File,
	}, nil
}

// fakePassVerifier always passes, recording a single named check for provenance.
type fakePassVerifier struct{}

func (fakePassVerifier) Verify(context.Context, engine.ProposedDiff, engine.Scope) engine.VerifyResult {
	return engine.VerifyResult{
		Passed: true,
		Checks: []engine.CheckResult{{Name: "compile", Passed: true, Detail: "build ok"}},
	}
}

// fakeFailVerifier always fails on a named check, so a test can assert the
// failing check is surfaced in ant.skipped.
type fakeFailVerifier struct {
	check string
}

func (v fakeFailVerifier) Verify(context.Context, engine.ProposedDiff, engine.Scope) engine.VerifyResult {
	name := v.check
	if name == "" {
		name = "compile"
	}
	return engine.VerifyResult{
		Passed: false,
		Checks: []engine.CheckResult{{Name: name, Passed: false, Detail: name + " failed: build broke"}},
	}
}

// fakeErrFixer always errors, to exercise the fix-failed → skip path.
type fakeErrFixer struct{}

func (fakeErrFixer) Fix(context.Context, engine.FixTask) (engine.ProposedDiff, error) {
	return engine.ProposedDiff{}, context.DeadlineExceeded
}

// collect drains a subscription into a slice once the bus closes. Callers run it
// in a goroutine, close the bus after the run, then read the returned channel.
func collect(sub *events.Subscription) <-chan []events.Event {
	done := make(chan []events.Event, 1)
	go func() {
		var got []events.Event
		for ev := range sub.C {
			got = append(got, ev)
		}
		done <- got
	}()
	return done
}

// countType returns how many events of a given type are in the slice.
func countType(evs []events.Event, t events.Type) int {
	n := 0
	for _, ev := range evs {
		if ev.Type == t {
			n++
		}
	}
	return n
}

// firstOf returns the first event of the given type, or a zero Event and false.
func firstOf(evs []events.Event, t events.Type) (events.Event, bool) {
	for _, ev := range evs {
		if ev.Type == t {
			return ev, true
		}
	}
	return events.Event{}, false
}
