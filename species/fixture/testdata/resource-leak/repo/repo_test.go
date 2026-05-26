package resourceleak

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCountBytes exercises CountBytes on both the success path (a real file) and
// the error path (a missing file). The post-fix code adds `defer f.Close()`; the
// signature is unchanged, so this test holds before and after the fix and proves
// behavior is preserved on every path. tests:affected (TECHSPEC §5.3.1) selects
// this package and runs THIS test against the patched scratch tree, so the
// recorded close-on-all-paths fix is accepted only if it compiles and keeps this
// behavior green — no live model.
func TestCountBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	got, err := CountBytes(path)
	if err != nil {
		t.Fatalf("CountBytes(%q) returned unexpected error: %v", path, err)
	}
	if want := 5; got != want {
		t.Fatalf("CountBytes(%q) = %d, want %d", path, got, want)
	}

	if _, err := CountBytes(filepath.Join(dir, "does-not-exist.txt")); err == nil {
		t.Fatal("CountBytes on a missing file should return an error")
	}
}
