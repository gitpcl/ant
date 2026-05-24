package stage_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/stage"
	local "github.com/gitpcl/ant/internal/engine/store"
)

// newRunStore returns a local Store rooted at base with runID already saved, so
// staging has a run to stage against (the Store rejects staging against an
// unsaved run).
func newRunStore(t *testing.T, base, runID string) *local.Store {
	t.Helper()
	st := local.New(base)
	if err := st.SaveRun(engine.Run{ID: runID, StartedAt: "2026-05-24T00:00:00Z"}); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	return st
}

func sampleDiff(fixer string) engine.ProposedDiff {
	return engine.ProposedDiff{
		Files: []engine.FileDiff{{
			Path:  "main.go",
			Patch: "--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,2 @@\n-import \"os\"\n",
		}},
		Fixer:     fixer,
		Rationale: "removed the unused os import",
	}
}

// TestAddDoesNotTouchWorkingTree is the load-bearing acceptance test: staging a
// diff must not modify the working tree. We hash the entire working-tree subtree
// (everything EXCEPT the .ant state dir the Store writes into) before and after
// staging and assert the hash is identical.
func TestAddDoesNotTouchWorkingTree(t *testing.T) {
	root := t.TempDir()

	// Seed a working tree with a source file that a fix would target.
	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	src := filepath.Join(work, "main.go")
	original := []byte("package main\n\nimport \"os\"\n\nfunc main() {}\n")
	if err := os.WriteFile(src, original, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	before := hashTree(t, work)

	st := newRunStore(t, root, "run-1")
	area := stage.New(st, "run-1")
	if err := area.Add(sampleDiff("deterministic (delete-match)")); err != nil {
		t.Fatalf("Add: %v", err)
	}

	after := hashTree(t, work)
	if before != after {
		t.Errorf("staging modified the working tree: hash %s -> %s", before, after)
	}

	// The source file content is byte-identical (the diff was recorded, not applied).
	got, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("re-read src: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("source file content changed after staging:\n got: %q\nwant: %q", got, original)
	}
}

// TestListRoundTripsProvenance proves List returns staged diffs in order with
// the Fixer (provenance) and Rationale intact, including across a fresh Store
// reading the same base (state survives a process restart).
func TestListRoundTripsProvenance(t *testing.T) {
	root := t.TempDir()
	st := newRunStore(t, root, "run-2")
	area := stage.New(st, "run-2")

	want := []engine.ProposedDiff{
		sampleDiff("deterministic (delete-match)"),
		sampleDiff("rawmodel (qwen2.5-coder)"),
	}
	for _, d := range want {
		if err := area.Add(d); err != nil {
			t.Fatalf("Add %q: %v", d.Fixer, err)
		}
	}

	// Re-open the store from the same base: nothing is held in memory, so this
	// proves persistence, not an in-process cache.
	reopened := stage.New(local.New(root), "run-2")
	got, err := reopened.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("List returned %d diffs, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Fixer != want[i].Fixer {
			t.Errorf("diff %d provenance: got %q, want %q", i, got[i].Fixer, want[i].Fixer)
		}
		if got[i].Rationale != want[i].Rationale {
			t.Errorf("diff %d rationale: got %q, want %q", i, got[i].Rationale, want[i].Rationale)
		}
		if len(got[i].Files) != len(want[i].Files) || got[i].Files[0].Patch != want[i].Files[0].Patch {
			t.Errorf("diff %d files not round-tripped", i)
		}
	}

	if n, err := reopened.Count(); err != nil || n != len(want) {
		t.Errorf("Count = %d, %v; want %d, nil", n, err, len(want))
	}
}

func TestAddRejectsUnattributedDiff(t *testing.T) {
	root := t.TempDir()
	area := stage.New(newRunStore(t, root, "run-3"), "run-3")

	t.Run("empty provenance", func(t *testing.T) {
		d := sampleDiff("")
		if err := area.Add(d); err == nil {
			t.Error("Add accepted a diff with empty Fixer (provenance is mandatory)")
		}
	})
	t.Run("empty file set", func(t *testing.T) {
		d := sampleDiff("deterministic")
		d.Files = nil
		if err := area.Add(d); err == nil {
			t.Error("Add accepted a diff with no file changes")
		}
	})
}

func TestListUnknownRunIsTypedError(t *testing.T) {
	area := stage.New(local.New(t.TempDir()), "never-saved")
	if _, err := area.List(); !errors.Is(err, engine.ErrRunNotFound) {
		t.Errorf("List on unknown run: got %v, want errors.Is ErrRunNotFound", err)
	}
}

// hashTree returns a stable hash over the file contents and relative paths under
// dir, so an unchanged tree hashes identically before and after an operation.
func hashTree(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	sort.Strings(paths)
	for _, p := range paths {
		rel, _ := filepath.Rel(dir, p)
		h.Write([]byte(rel))
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}
