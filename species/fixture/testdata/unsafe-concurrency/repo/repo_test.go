package unsafeconcurrency

import "testing"

// TestCountUp exercises the post-fix CountUp, which after the fix synchronizes
// the shared increment (sync.Mutex) and waits for every goroutine (sync.WaitGroup)
// so it deterministically returns n. tests:affected (TECHSPEC §5.3.1) selects this
// package and runs THIS test against the patched scratch tree; CI runs the suite
// under `go test -race`, so the recorded fix is accepted only if it compiles,
// returns the correct count, AND is race-free — proving the LLM species'
// propose-only concurrency fix is trustworthy without a live model.
func TestCountUp(t *testing.T) {
	if got, want := CountUp(100), 100; got != want {
		t.Fatalf("CountUp(100) = %d, want %d", got, want)
	}
	if got, want := CountUp(0), 0; got != want {
		t.Fatalf("CountUp(0) = %d, want %d", got, want)
	}
}
