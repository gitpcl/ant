package longfunc

import "testing"

// TestProcess pins Process's result across inputs. tests:affected runs this
// against the patched scratch tree, so the extraction MUST keep Process's return
// value identical — a helper that reads the wrong variables or reorders the
// arithmetic fails the gate.
func TestProcess(t *testing.T) {
	cases := []struct {
		a, b, c, d int
		want       int
	}{
		{1, 2, 3, 4, computeRef(1, 2, 3, 4)},
		{0, 0, 0, 0, 0},
		{2, 3, 5, 7, computeRef(2, 3, 5, 7)},
		{-1, 4, -2, 6, computeRef(-1, 4, -2, 6)},
	}
	for _, c := range cases {
		if got := Process(c.a, c.b, c.c, c.d); got != c.want {
			t.Fatalf("Process(%d,%d,%d,%d) = %d, want %d", c.a, c.b, c.c, c.d, got, c.want)
		}
	}
}

// computeRef is the independent reference for the expected value, so the test
// asserts the actual arithmetic rather than echoing the implementation.
func computeRef(a, b, c, d int) int {
	x := a + b
	y := b + c
	z := c + d
	return x*y + y*z
}
