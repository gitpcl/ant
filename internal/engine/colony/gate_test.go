package colony_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/colony"
	"github.com/gitpcl/ant/internal/engine/events"
	"github.com/gitpcl/ant/internal/engine/verify"
)

// bigDiffFixer emits a ProposedDiff far over any diff-bounded line limit, so the
// real diff-bounded gate (first in the chain) rejects it. It exercises the gate
// end to end through the colony, not a hand-rolled fake verifier.
type bigDiffFixer struct{}

func (bigDiffFixer) Fix(_ context.Context, task engine.FixTask) (engine.ProposedDiff, error) {
	var b strings.Builder
	b.WriteString("--- a/" + task.Finding.File + "\n+++ b/" + task.Finding.File + "\n@@ -1,0 +1,500 @@\n")
	for i := 0; i < 500; i++ {
		b.WriteString("+runaway rewrite line\n")
	}
	return engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: task.Finding.File, Patch: b.String()}},
		Fixer: "runaway-fixer",
	}, nil
}

// renderJSONStream renders a finished bus's events to a --json byte stream the
// way the CLI front door does, so the test asserts the skip reason survives into
// the machine-readable output the front doors parse.
func renderJSONStream(t *testing.T, run func(*events.Bus)) []byte {
	t.Helper()
	bus := events.NewBus()
	sub := bus.Subscribe()
	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- events.RenderJSON(&buf, sub) }()
	run(bus)
	bus.Close()
	if err := <-done; err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	return buf.Bytes()
}

// TestGateDiffBoundedFailSkipsAndSurfacesInJSON is the feature-#4 acceptance
// test: a fix that fails a REQUIRED verifier (diff-bounded, run FIRST by the
// real gate) is NOT staged, emits ant.skipped, and the failing check + reason
// appear in the rendered --json stream. No silent drops.
func TestGateDiffBoundedFailSkipsAndSurfacesInJSON(t *testing.T) {
	st := newRun(t, "gate-1")

	// The real ordered gate: diff-bounded FIRST (cap 50 lines), then a verifier
	// that would pass — proving the cheap gate rejects the runaway diff before the
	// expensive one runs.
	gate := verify.NewGate(
		verify.Limits{MaxChangedLines: 50, MaxChangedFiles: 10},
		fakePassVerifier{}, // stands in for compile/detector-clears
	)

	var res colony.Result
	jsonOut := renderJSONStream(t, func(bus *events.Bus) {
		var err error
		res, err = colony.Run(context.Background(), bus, colony.Options{
			Scope: engine.Scope{Root: "."},
			Ants: []colony.Ant{{
				Finding:  finding("runaway.go", 1, engine.SeverityHigh),
				Fixer:    bigDiffFixer{},
				Verifier: gate,
			}},
			Store:       st,
			RunID:       "gate-1",
			Concurrency: 1,
		})
		if err != nil {
			t.Errorf("colony.Run: %v", err)
		}
	})

	// Nothing staged; counted as a skip.
	if res.Verified != 0 || res.Skipped != 1 || res.Staged != 0 {
		t.Errorf("result = %+v; want Verified 0, Skipped 1, Staged 0", res)
	}
	staged, _ := st.ListStaged("gate-1")
	if len(staged) != 0 {
		t.Errorf("a gate failure must stage nothing; got %d staged", len(staged))
	}

	// The --json stream must carry an ant.skipped naming the diff-bounded gate and
	// a non-empty reason — the skip is visible to the front doors that parse it.
	var sawSkip bool
	for _, line := range bytes.Split(bytes.TrimSpace(jsonOut), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var ev events.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("unmarshal --json line %q: %v", line, err)
		}
		if ev.Type != events.TypeAntSkipped {
			continue
		}
		sawSkip = true
		if ev.AntSkipped.FailedCheck.Name != verify.CheckDiffBounded {
			t.Errorf("failing check = %q, want %q (diff-bounded runs first)", ev.AntSkipped.FailedCheck.Name, verify.CheckDiffBounded)
		}
		if ev.AntSkipped.Reason == "" {
			t.Error("ant.skipped in --json must carry a reason")
		}
	}
	if !sawSkip {
		t.Fatal("--json stream contained no ant.skipped — a skip was swallowed (PRD §6.3 violation)")
	}
	// The reason text itself must be present in the raw --json bytes (front doors
	// read it verbatim).
	if !bytes.Contains(jsonOut, []byte("runaway edits")) {
		t.Errorf("--json output should contain the diff-bounded reason; got:\n%s", jsonOut)
	}
}

// TestGateDiffBoundedRunsBeforeDownstream proves the ORDERING through the colony:
// when the runaway diff is rejected by diff-bounded, a downstream verifier that
// would PANIC if run is never invoked — so the cheap gate truly short-circuits
// before the expensive ones (TECHSPEC §8.1).
func TestGateDiffBoundedRunsBeforeDownstream(t *testing.T) {
	st := newRun(t, "gate-order-1")
	gate := verify.NewGate(
		verify.Limits{MaxChangedLines: 50},
		panicVerifier{}, // must never run, or the test panics
	)

	res, err := colony.Run(context.Background(), events.NewBus(), colony.Options{
		Ants: []colony.Ant{{
			Finding:  finding("runaway.go", 1, engine.SeverityHigh),
			Fixer:    bigDiffFixer{},
			Verifier: gate,
		}},
		Store:       st,
		RunID:       "gate-order-1",
		Concurrency: 1,
	})
	if err != nil {
		t.Fatalf("colony.Run: %v", err)
	}
	if res.Skipped != 1 {
		t.Errorf("runaway diff should be skipped by diff-bounded; result = %+v", res)
	}
}

// panicVerifier panics if its Verify is ever called — used to prove the gate
// short-circuited before reaching it.
type panicVerifier struct{}

func (panicVerifier) Verify(context.Context, engine.ProposedDiff, engine.Scope) engine.VerifyResult {
	panic("downstream verifier must not run after diff-bounded rejects the diff")
}
