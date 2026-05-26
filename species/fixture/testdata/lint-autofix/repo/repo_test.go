package lintautofix

import "testing"

// TestAdd gives the lint-autofix fixture a real test so the tests:affected gate
// runs the package's tests over the post-fix scratch tree (not a vacuous pass).
// The autofix only strips trailing whitespace, so behavior is unchanged and the
// test stays green — proving the gate accepts a safe autofix.
func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2,3) = %d, want 5", got)
	}
}
