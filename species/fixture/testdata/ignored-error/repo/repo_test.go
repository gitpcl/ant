package ignorederror

import "testing"

// TestPort exercises the post-fix Port, which returns (int, error) after the
// ignored-error fix binds and propagates parsePort's error. tests:affected
// (TECHSPEC §5.3.1) selects this package and runs THIS test against the patched
// scratch tree, so the recorded fix is only accepted if it both compiles and
// keeps this behavior green — proving the LLM species' propose-only fix is
// trustworthy without a live model.
func TestPort(t *testing.T) {
	got, err := Port("8080")
	if err != nil {
		t.Fatalf("Port(\"8080\") returned unexpected error: %v", err)
	}
	if want := 8080; got != want {
		t.Fatalf("Port(\"8080\") = %d, want %d", got, want)
	}

	if _, err := Port("not-a-port"); err == nil {
		t.Fatal("Port(\"not-a-port\") should return an error for an unparseable port")
	}
}
