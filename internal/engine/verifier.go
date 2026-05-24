package engine

import "context"

// Verifier deterministically checks a proposed diff is safe (TECHSPEC §5.3). A
// fix that fails any required verifier is skipped and never applied; the skip
// and its reason are surfaced through the event bus.
//
// Built-in kinds: compile, tests:affected, tests:all, detector-clears,
// diff-bounded, command (script escape hatch). Each runner asserts interface
// satisfaction at compile time, e.g.:
//
//	var _ engine.Verifier = (*compileVerifier)(nil)
type Verifier interface {
	Verify(ctx context.Context, diff ProposedDiff, scope Scope) VerifyResult
}
