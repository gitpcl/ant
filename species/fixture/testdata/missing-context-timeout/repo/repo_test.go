package missingcontexttimeout

import "testing"

// TestFetch exercises Fetch, whose signature is unchanged by the fix (the fix
// adds a bounded context internally). tests:affected (TECHSPEC §5.3.1) selects
// this package and runs THIS test against the patched scratch tree, so the
// recorded timeout fix is accepted only if it compiles and keeps this behavior
// green — proving the LLM species' propose-only fix is trustworthy without a
// live model.
func TestFetch(t *testing.T) {
	got, err := Fetch("abc")
	if err != nil {
		t.Fatalf("Fetch(\"abc\") returned unexpected error: %v", err)
	}
	if want := "value-for-abc"; got != want {
		t.Fatalf("Fetch(\"abc\") = %q, want %q", got, want)
	}
}
