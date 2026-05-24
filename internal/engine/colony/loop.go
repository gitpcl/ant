// Package colony is the heart of the engine: the run loop that turns findings
// into verified, staged diffs (TECHSPEC §8), and the worker pool that runs ants
// concurrently (TECHSPEC §8.1). It composes the existing seams — the Fixer and
// Verifier interfaces, the event bus, and the staging area over the Store — and
// owns the per-ant lifecycle: build a FixTask, call the Fixer, run the
// Verifier, and on pass stage + emit ant.verified, on fail discard + emit
// ant.skipped with the failing check.
//
// It lives in its own package (not engine) for the same reason scout does: the
// engine package stays pure types+interfaces and avoids an import cycle with
// engine/events (events imports engine for its payloads).
package colony

import (
	"context"
	"fmt"
	"time"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/events"
	"github.com/gitpcl/ant/internal/engine/stage"
)

// Ant is one unit of work for the colony: a finding plus the Fixer and Verifier
// resolved for its owning species. The caller (the CLI/engine boundary) resolves
// species → adapters and hands the colony fully-wired Ants, exactly as scout
// takes pre-built NamedDetectors. This keeps the colony dependent only on the
// engine.Fixer / engine.Verifier interfaces, not on the species registry.
type Ant struct {
	Finding  engine.Finding
	Fixer    engine.Fixer
	Verifier engine.Verifier
}

// Options parameterizes a colony run.
//
// Concurrency is the number of worker ants; values < 1 are normalized to 1.
// (The CLI passes the resolved config value, default NumCPU — TECHSPEC §8 step
// 3.) Store + RunID back the staging area; the run must already be saved in the
// Store before Run is called (staging against an unsaved run is an error).
type Options struct {
	Scope       engine.Scope
	Ants        []Ant
	Store       engine.Store
	RunID       string
	Concurrency int
	Now         func() time.Time
}

// Result summarizes a colony run for the caller's exit-code / summary decision.
type Result struct {
	RunID    string
	Verified int
	Skipped  int
	Staged   int
}

// Run executes the colony loop: it publishes run.start, schedules every Ant
// through the worker pool (each ant: build FixTask → Fix → Verify → stage|skip),
// then publishes run.end with aggregate counts. The bus is the single source of
// truth — the TUI and --json renderers both consume what this emits
// (TECHSPEC §3, §8, §11).
//
// Fix generation runs in parallel across the pool; verifier runs that touch
// shared build state are serialized behind a per-project mutex inside the pool
// (TECHSPEC §8.1). Staging (a read-modify-write Store append) is serialized
// behind the same mutex so concurrent ants never corrupt the staged set.
//
// A failing verifier is a SKIP, not a run error: the ant emits ant.skipped with
// the failing check and the loop continues. Run returns an error only on an
// operational failure (e.g. staging a verified diff fails) so the CLI maps it to
// exit code 2.
func Run(ctx context.Context, bus *events.Bus, opts Options) (Result, error) {
	clock := opts.Now
	if clock == nil {
		clock = time.Now
	}
	runID := opts.RunID
	if runID == "" {
		runID = fmt.Sprintf("colony-%d", clock().UTC().UnixNano())
	}

	bus.Publish(events.Event{
		Type:     events.TypeRunStart,
		RunStart: &events.RunStartPayload{RunID: runID, Scope: opts.Scope},
	})

	area := stage.New(opts.Store, runID)

	pool := newPool(opts.Concurrency)
	agg, err := pool.run(ctx, opts.Ants, &antRunner{
		runID: runID,
		scope: opts.Scope,
		bus:   bus,
		area:  area,
	})

	result := Result{
		RunID:    runID,
		Verified: agg.verified,
		Skipped:  agg.skipped,
		Staged:   agg.verified, // every verified ant stages exactly one diff
	}

	highest := highestSeverity(opts.Ants)
	runEnd := &events.RunEndPayload{
		RunID:           runID,
		Findings:        len(opts.Ants),
		Verified:        agg.verified,
		Skipped:         agg.skipped,
		HighestSeverity: highest.String(),
	}
	if err != nil {
		runEnd.Error = err.Error()
	}
	bus.Publish(events.Event{Type: events.TypeRunEnd, RunEnd: runEnd})

	return result, err
}

// antRunner holds the per-run collaborators an ant needs and processes a single
// Ant. The worker pool calls process from N goroutines; everything process
// touches that is shared (the Verifier's build state and the staging Store) is
// guarded by the pool's mutex, passed in as serialize.
type antRunner struct {
	runID string
	scope engine.Scope
	bus   *events.Bus
	area  *stage.Area
}

// outcome is what processing one ant produced, accumulated by the pool.
type outcome struct {
	verified bool
	skipped  bool
	err      error // operational failure (e.g. staging failed) — aborts the run
}

// process runs one ant's full lifecycle. serialize is invoked around the two
// shared-state sections — the build-state verifier run and the Store stage write
// — so they never overlap across workers (TECHSPEC §8.1). Fix generation runs
// outside serialize, fully parallel.
func (r *antRunner) process(ctx context.Context, antID int, ant Ant, serialize func(func())) outcome {
	r.bus.Publish(events.Event{
		Type:     events.TypeAntStart,
		AntStart: &events.AntStartPayload{RunID: r.runID, AntID: antID, Finding: ant.Finding},
	})

	// 1. Build the FixTask and generate the diff. PARALLEL — no shared state.
	task := buildFixTask(ant.Finding)
	diff, err := ant.Fixer.Fix(ctx, task)
	if err != nil {
		// A fixer that fails to produce a diff is a skip, not a run error: the
		// finding could not be fixed, surface it like a failed verifier so it is
		// never silently dropped (PRD §6.3).
		check := engine.CheckResult{Name: "fix", Passed: false, Detail: err.Error()}
		r.emitSkipped(antID, ant.Finding, check, engine.VerifyResult{Passed: false, Checks: []engine.CheckResult{check}})
		return outcome{skipped: true}
	}

	// 2. Verify the diff. SERIALIZED — verifiers touch shared build state.
	var vr engine.VerifyResult
	serialize(func() {
		vr = ant.Verifier.Verify(ctx, diff, r.scope)
	})

	if !vr.Passed {
		check := firstFailed(vr)
		r.emitSkipped(antID, ant.Finding, check, vr)
		return outcome{skipped: true}
	}

	// 3. Stage the verified diff. SERIALIZED — StageDiff is a read-modify-write
	// append on the Store; concurrent appends must not interleave.
	var stageErr error
	serialize(func() {
		stageErr = r.area.Add(diff)
	})
	if stageErr != nil {
		return outcome{err: fmt.Errorf("colony: stage verified diff for finding %s:%d: %w", ant.Finding.File, ant.Finding.Span.StartLine, stageErr)}
	}

	r.bus.Publish(events.Event{
		Type:        events.TypeAntVerified,
		AntVerified: &events.AntVerifiedPayload{RunID: r.runID, AntID: antID, Diff: diff, Verify: vr},
	})
	return outcome{verified: true}
}

// emitSkipped publishes ant.skipped carrying the failing check and a reason, so
// a skip is always visible (a trust signal, never a silent drop — PRD §6.3).
func (r *antRunner) emitSkipped(antID int, finding engine.Finding, failed engine.CheckResult, vr engine.VerifyResult) {
	r.bus.Publish(events.Event{
		Type: events.TypeAntSkipped,
		AntSkipped: &events.AntSkippedPayload{
			RunID:       r.runID,
			AntID:       antID,
			Finding:     finding,
			FailedCheck: failed,
			Reason:      failed.Detail,
			Verify:      vr,
		},
	})
}

// buildFixTask assembles a FixTask from a finding. The CodeContext mirrors the
// finding's location and snippet; the fixer adapters use the context (not a
// re-read of the tree) so the task is self-contained and the adapter stays
// stateless (TECHSPEC §10). The owning caller may enrich Context.Before/After
// and Prompt before scheduling; the colony defaults them from the finding.
func buildFixTask(f engine.Finding) engine.FixTask {
	return engine.FixTask{
		Finding: f,
		Context: engine.CodeContext{
			File:    f.File,
			Span:    f.Span,
			Snippet: f.Snippet,
		},
	}
}

// firstFailed returns the first failed CheckResult in a VerifyResult, or a
// generic failure if the result is marked failed without naming a check (a
// verifier should always name the gate, but the loop never panics on a bad one).
func firstFailed(vr engine.VerifyResult) engine.CheckResult {
	for _, c := range vr.Checks {
		if !c.Passed {
			return c
		}
	}
	return engine.CheckResult{Name: "verify", Passed: false, Detail: "verification failed"}
}

// highestSeverity returns the highest finding severity across the ants, for the
// run.end summary / CI gate.
func highestSeverity(ants []Ant) engine.Severity {
	highest := engine.SeverityUnknown
	for _, a := range ants {
		if a.Finding.Severity > highest {
			highest = a.Finding.Severity
		}
	}
	return highest
}
