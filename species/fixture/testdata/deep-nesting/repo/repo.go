package deepnesting

// Classify labels an access request. It is written with three levels of nested
// `if` (depth 3) — the deep-nesting smell — so the success path is buried under
// indentation and each exit condition is implicit. The deep-nesting species
// flags the outermost `if` and the recorded fix flattens it to guard clauses,
// preserving the exact result on every path (proven by repo_test.go, which
// exercises the success path AND each early-exit path).
func Classify(ok bool, n int, name string) string {
	if ok {
		if n > 0 {
			if name != "" {
				return "valid:" + name
			}
		}
	}
	return "invalid"
}
