package aislop

import "testing"

// TestSum exercises Sum, whose behavior is unchanged by the ai-slop fix (the
// redundant temporary is inlined to `return a + b`). tests:affected
// (TECHSPEC §5.3.1) selects this package and runs THIS test against the patched
// scratch tree, so the recorded fix is accepted only if it both compiles and
// keeps Sum's behavior identical — proving the fuzzy species' propose-only fix
// is trustworthy without a live model.
func TestSum(t *testing.T) {
	if got, want := Sum(2, 3), 5; got != want {
		t.Fatalf("Sum(2, 3) = %d, want %d", got, want)
	}
	if got, want := Sum(-4, 4), 0; got != want {
		t.Fatalf("Sum(-4, 4) = %d, want %d", got, want)
	}
}
