package unsafetempfile

import (
	"os"
	"testing"
)

// TestWriteCacheCreatesFile exercises the POST-FIX shape: after the unsafe-temp-
// file fix WriteCache uses os.CreateTemp, so it returns a real (unpredictable)
// path that exists and holds the written data. tests:affected runs this against
// the PATCHED scratch tree, so it asserts the secure-temp rewrite still creates
// and writes a temp file. The file is cleaned up after the check.
func TestWriteCacheCreatesFile(t *testing.T) {
	want := []byte("cache-payload")
	path, err := WriteCache(want)
	if err != nil {
		t.Fatalf("WriteCache returned error: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the temp file %q failed: %v", path, err)
	}
	if string(got) != string(want) {
		t.Fatalf("temp file content = %q, want %q", got, want)
	}
}

// TestWriteCacheUnpredictablePath confirms two calls produce DIFFERENT paths — the
// secure temp API picks an unpredictable name each time (the security property the
// fix introduces), so the path is no longer a fixed, guessable location.
func TestWriteCacheUnpredictablePath(t *testing.T) {
	p1, err := WriteCache([]byte("a"))
	if err != nil {
		t.Fatalf("first WriteCache: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(p1) })
	p2, err := WriteCache([]byte("b"))
	if err != nil {
		t.Fatalf("second WriteCache: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(p2) })
	if p1 == p2 {
		t.Fatalf("WriteCache returned the same path twice (%q) — the temp name is still predictable", p1)
	}
}
