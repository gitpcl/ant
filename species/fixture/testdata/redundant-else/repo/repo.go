package redundantelse

// classify SHOULD flatten: the if-branch ends in `return`, so the trailing
// `else` is redundant and can become a guard clause. This is the one finding the
// species targets.
func classify(n int) string {
	if n < 0 {
		return "neg"
	} else {
		return "pos"
	}
}

// describe MUST NOT flatten: the if-branch assigns and falls through (no
// terminating return), so dropping the else would change behavior. The pattern's
// required trailing `return` excludes it.
func describe(n int) string {
	out := "?"
	if n < 0 {
		out = "neg"
	} else {
		out = "pos"
	}
	return out
}

// chain MUST NOT flatten: an `else if` chain — the alternative is an if, not a
// plain `{ … }` block, so the pattern does not match.
func chain(n int) string {
	if n < 0 {
		return "neg"
	} else if n == 0 {
		return "zero"
	}
	return "pos"
}
