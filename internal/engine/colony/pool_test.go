package colony_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/colony"
	"github.com/gitpcl/ant/internal/engine/events"
)

// buildStateVerifier models a verifier that touches shared build state: it must
// NEVER run concurrently with another build-state verifier (TECHSPEC §8.1). It
// increments a live counter on entry and decrements on exit; if the counter ever
// exceeds 1, two verifiers overlapped and the test fails. A small sleep widens
// the window so an unserialized run would reliably trip the detector.
type buildStateVerifier struct {
	inside  atomic.Int32
	maxSeen atomic.Int32
	runs    atomic.Int64
}

func (v *buildStateVerifier) Verify(context.Context, engine.ProposedDiff, engine.Scope) engine.VerifyResult {
	cur := v.inside.Add(1)
	// Track the high-water mark of concurrent entries.
	for {
		prev := v.maxSeen.Load()
		if cur <= prev || v.maxSeen.CompareAndSwap(prev, cur) {
			break
		}
	}
	v.runs.Add(1)
	time.Sleep(50 * time.Microsecond) // widen the overlap window
	v.inside.Add(-1)
	return engine.VerifyResult{Passed: true, Checks: []engine.CheckResult{{Name: "compile", Passed: true}}}
}

// countingFixer records, per finding file, how many times it was fixed, so the
// test can assert every queued finding was processed EXACTLY once across all
// workers. Fix generation is the parallel section, so this also runs under
// -race against the shared map (guarded by a mutex).
type countingFixer struct {
	mu     sync.Mutex
	counts map[string]int
}

func newCountingFixer() *countingFixer { return &countingFixer{counts: map[string]int{}} }

func (f *countingFixer) Fix(_ context.Context, task engine.FixTask) (engine.ProposedDiff, error) {
	f.mu.Lock()
	f.counts[task.Finding.File]++
	f.mu.Unlock()
	return engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: task.Finding.File, Patch: "@@ fake @@\n"}},
		Fixer: "fake-fixer",
	}, nil
}

// TestPoolProcessesEachFindingExactlyOnceAndSerializesVerifiers is the
// concurrency-correctness test (run under -race): many findings across a
// multi-worker pool, asserting (1) every queued finding is fixed exactly once,
// (2) every verified one is staged exactly once, (3) build-state verifiers never
// overlap (max concurrent entries == 1), and (4) the verifier ran once per ant.
func TestPoolProcessesEachFindingExactlyOnceAndSerializesVerifiers(t *testing.T) {
	const n = 200
	st := newRun(t, "race-1")
	bus := events.NewBus(events.WithBuffer(2 * n)) // generous buffer; drained live below
	sub := bus.Subscribe()
	evDone := collect(sub)

	fixer := newCountingFixer()
	verifier := &buildStateVerifier{}

	ants := make([]colony.Ant, n)
	for i := range ants {
		ants[i] = colony.Ant{
			Finding:  finding(fmt.Sprintf("file-%03d.go", i), i+1, engine.SeverityLow),
			Fixer:    fixer,
			Verifier: verifier,
		}
	}

	res, err := colony.Run(context.Background(), bus, colony.Options{
		Ants:        ants,
		Store:       st,
		RunID:       "race-1",
		Concurrency: 8, // force real parallelism regardless of host NumCPU
	})
	bus.Close()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// (1) Each finding fixed exactly once.
	if got := len(fixer.counts); got != n {
		t.Errorf("distinct findings fixed = %d, want %d", got, n)
	}
	for file, c := range fixer.counts {
		if c != 1 {
			t.Errorf("finding %s fixed %d times, want exactly 1", file, c)
		}
	}

	// (2) Every verified ant staged exactly once → n staged diffs, no duplicates,
	// no losses (the read-modify-write append is serialized).
	staged, err := st.ListStaged("race-1")
	if err != nil {
		t.Fatalf("ListStaged: %v", err)
	}
	if len(staged) != n {
		t.Errorf("staged diffs = %d, want %d (no lost or duplicated appends)", len(staged), n)
	}
	if res.Verified != n || res.Staged != n {
		t.Errorf("result = %+v; want Verified %d, Staged %d", res, n, n)
	}

	// (3) Build-state verifiers never overlapped.
	if max := verifier.maxSeen.Load(); max > 1 {
		t.Errorf("build-state verifiers overlapped: max concurrent = %d, want 1 (TECHSPEC §8.1 serialization)", max)
	}
	// (4) Verifier ran once per ant.
	if got := verifier.runs.Load(); got != int64(n) {
		t.Errorf("verifier ran %d times, want %d", got, n)
	}

	// Event stream: exactly n ant.start and n ant.verified, no skips.
	evs := <-evDone
	if c := countType(evs, events.TypeAntStart); c != n {
		t.Errorf("ant.start count = %d, want %d", c, n)
	}
	if c := countType(evs, events.TypeAntVerified); c != n {
		t.Errorf("ant.verified count = %d, want %d", c, n)
	}
	if c := countType(evs, events.TypeAntSkipped); c != 0 {
		t.Errorf("ant.skipped count = %d, want 0", c)
	}
}

// TestConcurrencyDefaultsToNumCPU verifies a zero/negative concurrency is
// normalized (defaults to NumCPU, never 0) so the pool always has at least one
// worker and still processes every finding exactly once.
func TestConcurrencyDefaultsToNumCPU(t *testing.T) {
	const n = 20
	st := newRun(t, "default-conc")
	bus := events.NewBus(events.WithBuffer(4 * n))
	sub := bus.Subscribe()
	evDone := collect(sub)

	fixer := newCountingFixer()
	ants := make([]colony.Ant, n)
	for i := range ants {
		ants[i] = colony.Ant{
			Finding:  finding(fmt.Sprintf("z-%02d.go", i), i+1, engine.SeverityLow),
			Fixer:    fixer,
			Verifier: fakePassVerifier{},
		}
	}

	res, err := colony.Run(context.Background(), bus, colony.Options{
		Ants:        ants,
		Store:       st,
		RunID:       "default-conc",
		Concurrency: 0, // unset → must default to NumCPU, not deadlock on 0 workers
	})
	bus.Close()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Verified != n {
		t.Errorf("verified = %d, want %d", res.Verified, n)
	}
	for file, c := range fixer.counts {
		if c != 1 {
			t.Errorf("finding %s fixed %d times, want 1", file, c)
		}
	}
	evs := <-evDone
	if c := countType(evs, events.TypeAntVerified); c != n {
		t.Errorf("ant.verified = %d, want %d", c, n)
	}
}

// TestEmptyRunIsWellFormed proves a run with no ants still emits run.start →
// run.end and returns zeroed counts (no deadlock on an empty queue).
func TestEmptyRunIsWellFormed(t *testing.T) {
	st := newRun(t, "empty")
	bus := events.NewBus()
	sub := bus.Subscribe()
	evDone := collect(sub)

	res, err := colony.Run(context.Background(), bus, colony.Options{
		Ants: nil, Store: st, RunID: "empty", Concurrency: 4,
	})
	bus.Close()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Verified != 0 || res.Skipped != 0 || res.Staged != 0 {
		t.Errorf("empty run result = %+v; want all zero", res)
	}
	evs := <-evDone
	if len(evs) != 2 || evs[0].Type != events.TypeRunStart || evs[1].Type != events.TypeRunEnd {
		t.Errorf("empty run events = %v; want exactly [run.start, run.end]", typesOf(evs))
	}
}

func typesOf(evs []events.Event) []events.Type {
	out := make([]events.Type, len(evs))
	for i, e := range evs {
		out[i] = e.Type
	}
	return out
}
