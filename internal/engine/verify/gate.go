package verify

import "github.com/gitpcl/ant/internal/engine"

// NewGate composes the required verifiers into the ordered skip-and-surface gate
// the colony runs per ant (TECHSPEC §5.3, §8.1). It enforces the one ordering
// rule the spec fixes: diff-bounded runs FIRST (cheap; it kills runaway edits
// before the expensive compile/detector-clears runs). The remaining required
// verifiers run in the order given, and the gate short-circuits on the first
// failure (via the underlying Chain).
//
// The result is an engine.Verifier the colony already knows how to run: on a
// failing gate the colony discards the diff and emits ant.skipped carrying the
// failing CheckResult, so the skip reason flows into both the --json stream and
// the TUI renderer (PRD §6.3 — a skip is a trust signal, never a hidden error).
// The diff-bounded verifier is constructed here from the caller's resolved
// limits so the gate's size cap is a config value, not a hardcoded constant.
//
// rest holds the species' other required verifiers (e.g. compile,
// detector-clears) already constructed by the caller, in their declared order.
// diff-bounded is always prepended regardless of where the species listed it, so
// the cheap gate cannot be accidentally ordered after an expensive one.
func NewGate(limits Limits, rest ...engine.Verifier) engine.Verifier {
	ordered := make([]engine.Verifier, 0, len(rest)+1)
	ordered = append(ordered, NewDiffBounded(limits))
	ordered = append(ordered, rest...)
	return NewChain(ordered...)
}
