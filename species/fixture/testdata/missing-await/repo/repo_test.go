package missingawait

import "testing"

// TestSumSquares pins SumSquares' result. Pre-fix it is racy and returns a
// dropped/partial total; post-fix it deterministically sums every square.
// tests:affected (TECHSPEC §5.3.1) selects this package and runs THIS test
// against the patched scratch tree (the suite runs with -race), so the recorded
// await/synchronize fix is accepted only if it compiles, is race-clean, and
// produces the correct total — the LLM species' propose-only fix proven safe
// without a live model.
func TestSumSquares(t *testing.T) {
	got := SumSquares([]int{1, 2, 3, 4})
	if want := 1 + 4 + 9 + 16; got != want {
		t.Fatalf("SumSquares = %d, want %d", got, want)
	}
}
