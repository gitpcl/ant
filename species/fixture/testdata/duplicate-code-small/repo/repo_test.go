package dup

import "testing"

// TestScores pins both callers' results, including the clamp boundaries (below 0,
// above 100, and in-range). tests:affected runs this against the patched scratch
// tree, so an extraction that changes either caller's clamping fails the gate.
func TestScores(t *testing.T) {
	cases := []struct {
		name string
		fn   func(int) int
		raw  int
		want int
	}{
		{"A-in-range", ScoreA, 10, 20},
		{"A-clamp-high", ScoreA, 80, 100},
		{"A-clamp-low", ScoreA, -5, 0},
		{"B-in-range", ScoreB, 10, 20},
		{"B-clamp-high", ScoreB, 95, 100},
		{"B-clamp-low", ScoreB, -50, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.fn(c.raw); got != c.want {
				t.Fatalf("%s(%d) = %d, want %d", c.name, c.raw, got, c.want)
			}
		})
	}
}
