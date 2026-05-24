package colony

import (
	"context"
	"runtime"
	"sync"
)

// pool is the colony's worker pool (TECHSPEC §8.1). N ant goroutines pull
// findings from a buffered channel; fix GENERATION runs fully in parallel, but
// the sections that touch shared build state — the verifier run and the staging
// append — are serialized behind one per-project mutex so build-state verifiers
// never overlap and the staged set is never corrupted by interleaved appends.
type pool struct {
	workers int

	// buildState serializes every section that touches shared project build
	// state or the staged set. It is the single per-project lock TECHSPEC §8.1
	// requires: compile/tests:* verifiers cannot run concurrently, and Store
	// appends cannot interleave.
	buildState sync.Mutex
}

// tally is the lock-free value carrying a run's aggregate counts back to the
// caller. It deliberately holds no mutex so it can be returned and copied
// freely (go vet flags returning a lock-bearing struct by value).
type tally struct {
	verified int
	skipped  int
	firstErr error
}

// aggregate accumulates per-ant outcomes across workers. It is guarded by its
// own mutex (distinct from buildState) so tallying never serializes with the
// build-state critical section. snapshot() copies the counts out into a
// lock-free tally for return.
type aggregate struct {
	mu sync.Mutex
	t  tally
}

func (a *aggregate) add(o outcome) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case o.err != nil:
		if a.t.firstErr == nil {
			a.t.firstErr = o.err
		}
	case o.verified:
		a.t.verified++
	case o.skipped:
		a.t.skipped++
	}
}

func (a *aggregate) snapshot() tally {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.t
}

// newPool builds a pool with the given worker count. A count < 1 falls back to
// NumCPU (TECHSPEC §8 step 3 default), and NumCPU itself can never be < 1.
func newPool(workers int) *pool {
	if workers < 1 {
		workers = runtime.NumCPU()
		if workers < 1 {
			workers = 1
		}
	}
	return &pool{workers: workers}
}

// run schedules every ant across the worker goroutines and returns the
// aggregated outcome once all are processed. Each ant is processed EXACTLY once:
// it is sent to the buffered queue exactly once and consumed by exactly one
// worker. The buffer is sized to the work count so the producer never blocks and
// the queue holds the whole batch (embarrassingly parallel; trails-based
// re-prioritization is a later, flag-gated feature — TECHSPEC §8.2).
//
// serialize, handed to each ant's process call, runs its closure under the
// pool's per-project mutex. That is where build-state verifiers and Store
// appends are mutually excluded across workers (TECHSPEC §8.1) while fix
// generation outside it stays parallel.
func (p *pool) run(ctx context.Context, ants []Ant, runner *antRunner) (tally, error) {
	var agg aggregate
	if len(ants) == 0 {
		return tally{}, nil
	}

	queue := make(chan job, len(ants))
	for i, a := range ants {
		// antID is the work-item index (1-based) so the same finding always maps
		// to the same ant id in the event stream regardless of which worker
		// goroutine picks it up — stable for golden tests and TUI lanes.
		queue <- job{antID: i + 1, ant: a}
	}
	close(queue) // workers drain until the channel is closed and empty

	serialize := func(fn func()) {
		p.buildState.Lock()
		defer p.buildState.Unlock()
		fn()
	}

	var wg sync.WaitGroup
	for w := 0; w < p.workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range queue {
				agg.add(runner.process(ctx, j.antID, j.ant, serialize))
			}
		}()
	}
	wg.Wait()

	snap := agg.snapshot()
	return snap, snap.firstErr
}

// job is one queued work item: the stable ant id and the wired ant.
type job struct {
	antID int
	ant   Ant
}
