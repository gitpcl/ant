package nilderef

import "testing"

// TestBalance exercises the post-fix Balance, which returns (int, error) after
// the nil-deref guard is added. tests:affected (TECHSPEC §5.3.1) selects this
// package and runs THIS test against the patched scratch tree, so the recorded
// fix is only accepted if it both compiles and keeps this behavior green —
// proving the LLM species' propose-only fix is trustworthy without a live model.
func TestBalance(t *testing.T) {
	got, err := Balance(5)
	if err != nil {
		t.Fatalf("Balance(5) returned unexpected error: %v", err)
	}
	if want := 50; got != want {
		t.Fatalf("Balance(5) = %d, want %d", got, want)
	}

	if _, err := Balance(0); err == nil {
		t.Fatal("Balance(0) should return an error for an unknown account")
	}
}
