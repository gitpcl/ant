package aislop

// Sum is the ai-slop smell: it declares a temporary `result` only to return it
// on the very next statement. The redundant intermediate adds nothing — a
// recognizable low-signal AI-boilerplate tic. The ai-slop detector NOMINATES
// this shape (a `return $V` that follows a `$V := $EXPR`); the recorded LLM fix
// inlines it to `return a + b`, and the verifier gate (compile + tests:affected
// + detector-clears) confirms the rewrite is behavior-preserving and that the
// smell is gone.
//
// ai-slop is a FUZZY, candidate-tier classifier and ships DISABLED by default
// (species.toml enabled = false); the harness exercises it directly because the
// detect→fix→verify pipeline is independent of the runtime enabled flag (which
// is a resolution-time concern), not because the species runs in a normal scan.
func Sum(a, b int) int {
	result := a + b
	return result
}
