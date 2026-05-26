package uncheckedassertion

import "testing"

// TestAsString exercises the post-fix AsString, which returns (string, error)
// after the fix switches to the comma-ok form. tests:affected (TECHSPEC §5.3.1)
// selects this package and runs THIS test against the patched scratch tree, so
// the recorded fix is only accepted if it both compiles and keeps this behavior
// green — proving the LLM species' propose-only fix is trustworthy without a
// live model.
func TestAsString(t *testing.T) {
	got, err := AsString("hello")
	if err != nil {
		t.Fatalf("AsString(\"hello\") returned unexpected error: %v", err)
	}
	if want := "hello"; got != want {
		t.Fatalf("AsString(\"hello\") = %q, want %q", got, want)
	}

	if _, err := AsString(42); err == nil {
		t.Fatal("AsString(42) should return an error for a non-string value")
	}
}
