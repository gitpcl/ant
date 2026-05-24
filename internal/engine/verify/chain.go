package verify

import (
	"context"

	"github.com/gitpcl/ant/internal/engine"
)

// chainVerifier runs an ordered list of required verifiers and short-circuits on
// the first failure. It is the mechanism that enforces the verifier ORDER the
// colony gate depends on: diff-bounded must run FIRST (cheap, kills runaway edits
// before the expensive compile/detector-clears runs — TECHSPEC §8.1). The
// aggregate VerifyResult.Passed is true only when EVERY required check passes; a
// fix whose result is not Passed is skipped and never applied (TECHSPEC §5.3).
//
// On failure the chain returns immediately, carrying the checks that ran up to
// and including the failing one — so the colony's firstFailed sees the real gate
// that fired and the surfaced skip reason is the right one. Checks that never ran
// (because an earlier gate failed) are intentionally absent: the diff was already
// rejected, so running them would waste the expensive build/detector work the
// ordering exists to avoid.
type chainVerifier struct {
	verifiers []engine.Verifier
}

// compile-time assertion that chainVerifier satisfies engine.Verifier.
var _ engine.Verifier = (*chainVerifier)(nil)

// NewChain composes required verifiers into one ordered gate. Pass them in the
// order they must run; the colony builds the chain with diff-bounded first.
func NewChain(verifiers ...engine.Verifier) engine.Verifier {
	return &chainVerifier{verifiers: verifiers}
}

// Verify runs each verifier in order, accumulating their checks, and returns the
// moment one fails (short-circuit). An empty chain passes with no checks — a
// species that declares no verifiers has nothing to gate on.
func (c *chainVerifier) Verify(ctx context.Context, diff engine.ProposedDiff, scope engine.Scope) engine.VerifyResult {
	var checks []engine.CheckResult
	for _, v := range c.verifiers {
		res := v.Verify(ctx, diff, scope)
		checks = append(checks, res.Checks...)
		if !res.Passed {
			return engine.VerifyResult{Passed: false, Checks: checks}
		}
	}
	return engine.VerifyResult{Passed: true, Checks: checks}
}
