package engine

import "context"

// Fixer turns a localized finding into a proposed diff (TECHSPEC §5.2). The
// fix(task) -> diff shape is uniform across every adapter.
//
// Built-in adapters: deterministic (code transform, no LLM), claudecode,
// codex, pi (exec the harness in print/JSON mode, one task), rawmodel
// (OpenAI-compatible HTTP). Each adapter asserts interface satisfaction at
// compile time, e.g.:
//
//	var _ engine.Fixer = (*deterministicFixer)(nil)
type Fixer interface {
	Fix(ctx context.Context, task FixTask) (ProposedDiff, error)
}
