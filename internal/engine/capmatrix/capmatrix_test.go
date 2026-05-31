package capmatrix_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine/capmatrix"
)

// docPath is the capability-matrix doc relative to the repo root. The test
// resolves it from this package's location (two dirs up from internal/engine)
// so it does not depend on the test's working directory.
func docPath(t *testing.T) string {
	t.Helper()
	// internal/engine/capmatrix -> repo root is ../../../.. ? No: this file lives
	// in internal/engine/capmatrix, so the repo root is three levels up.
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return filepath.Join(root, "docs", "CAPABILITY-MATRIX.md")
}

// TestCapabilityMatrixDocMatchesMetadata is the doc-consistency (drift) gate:
// it re-renders the built-in capability matrix from the authoritative metadata
// (species.Resolved.Capabilities, via capmatrix.RenderBuiltins) and asserts the
// committed docs/CAPABILITY-MATRIX.md embeds EXACTLY that table between its
// generated markers. If a species is added/removed or any capability field
// changes, this fails until the doc is regenerated, so the matrix can never
// silently drift from the manifests (Sprint 022 Future-Proofing #5,
// acceptance: "docs matrix matches metadata").
//
// UPDATE_DOCS=1 rewrites the generated region in place (the golden-update
// pattern); CI never sets it, so a drift always fails rather than auto-accepts.
func TestCapabilityMatrixDocMatchesMetadata(t *testing.T) {
	want, err := capmatrix.RenderBuiltins()
	if err != nil {
		t.Fatalf("render built-in capability matrix: %v", err)
	}

	path := docPath(t)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read capability matrix doc %s: %v", path, err)
	}
	doc := string(raw)

	begin := strings.Index(doc, capmatrix.MarkerBegin)
	end := strings.Index(doc, capmatrix.MarkerEnd)
	if begin < 0 || end < 0 || end < begin {
		t.Fatalf("doc %s missing generated markers %q / %q (cannot locate the table region)",
			path, capmatrix.MarkerBegin, capmatrix.MarkerEnd)
	}

	if os.Getenv("UPDATE_DOCS") == "1" {
		updated := doc[:begin] + capmatrix.MarkerBegin + "\n" + want + capmatrix.MarkerEnd + doc[end+len(capmatrix.MarkerEnd):]
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			t.Fatalf("update capability matrix doc: %v", err)
		}
		t.Logf("updated %s", path)
		return
	}

	// The region between the markers (exclusive) must be exactly "\n" + want.
	got := doc[begin+len(capmatrix.MarkerBegin) : end]
	if got != "\n"+want {
		t.Errorf("capability matrix doc drifted from metadata.\n--- doc table ---\n%s\n--- want (from Capabilities) ---\n%s\nRegenerate with UPDATE_DOCS=1 go test ./internal/engine/capmatrix/ if this change is intended.",
			strings.TrimPrefix(got, "\n"), want)
	}
}
