package magicnum

import "testing"

// TestConversions pins the conversion results so the constant extraction cannot
// change the value. tests:affected runs this against the patched scratch tree, so
// a fix that uses the wrong constant or value fails the gate.
func TestConversions(t *testing.T) {
	if got := ToSeconds(2); got != 172800 {
		t.Fatalf("ToSeconds(2) = %d, want 172800", got)
	}
	if got := ToDays(172800); got != 2 {
		t.Fatalf("ToDays(172800) = %d, want 2", got)
	}
	if got := ToSeconds(0); got != 0 {
		t.Fatalf("ToSeconds(0) = %d, want 0", got)
	}
}
