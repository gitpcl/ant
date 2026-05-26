package dup

// ScoreA and ScoreB both compute a value and then clamp it into [0, 100] with the
// SAME copy-pasted two-`if` block — the small-duplicate smell. The
// duplicate-code-small species flags the clamp block in each function (it follows
// a local `v :=`). The recorded fix extracts a shared `clamp` helper and calls it
// from both, so the duplicated block lives in one place; both callers compute the
// identical result (proven by repo_test.go).
func ScoreA(raw int) int {
	v := raw * 2
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return v
}

func ScoreB(raw int) int {
	v := raw + 10
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return v
}
