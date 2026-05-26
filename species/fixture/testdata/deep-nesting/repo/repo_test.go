package deepnesting

import "testing"

// TestClassify exercises the POST-FIX (guard-clause-flattened) shape on every
// path: the success path and each of the three early-exit conditions. The
// deep-nesting fix MUST preserve these exact results — tests:affected runs this
// against the patched scratch tree, so a flatten that changes any return value
// (a wrong negation, a mis-ordered guard) fails the gate.
func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
		n    int
		in   string
		want string
	}{
		{"all-valid", true, 1, "alice", "valid:alice"},
		{"not-ok", false, 1, "alice", "invalid"},
		{"non-positive-n", true, 0, "alice", "invalid"},
		{"negative-n", true, -3, "alice", "invalid"},
		{"empty-name", true, 1, "", "invalid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.ok, c.n, c.in); got != c.want {
				t.Fatalf("Classify(%v, %d, %q) = %q, want %q", c.ok, c.n, c.in, got, c.want)
			}
		})
	}
}
