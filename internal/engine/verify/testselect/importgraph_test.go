package testselect

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
)

// materializeFixture copies testdata/igmod into a temp dir, renaming the
// .txt-suffixed sources to their real Go names so the fixture is a real,
// buildable module the live `go list` can analyze — WITHOUT the fixture being
// compiled as part of the ant module itself (the .txt suffix keeps it inert in
// the repo). Returns the temp module root.
func materializeFixture(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return nil
		}
		name := strings.TrimSuffix(rel, ".txt")
		target := filepath.Join(dst, name)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("materialize fixture: %v", err)
	}
	return dst
}

// TestImportGraphSelectsTransitiveImporters is the SPIKE that validates the
// TestSelector interface + strategy reporting against a real fixture module with
// the LIVE go toolchain (no coverage tooling needed) — the cheapest validation of
// the approach per the gate. Changing leaf must select leaf (its own tests) and
// mid (a transitive importer) and EXCLUDE unrelated (imports nothing changed),
// proving the selection is narrower than the full suite.
func TestImportGraphSelectsTransitiveImporters(t *testing.T) {
	root := materializeFixture(t, filepath.Join("testdata", "igmod"))

	sel := NewImportGraph(nil) // nil → live `go list`
	changes := []Change{{File: filepath.Join("leaf", "leaf.go"), Lines: []int{4}}}

	got, err := sel.Select(context.Background(), changes, engine.Scope{Root: root})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if !got.OK {
		t.Fatalf("expected OK selection, got OK=false")
	}

	pkgs := suffixes(got.Packages)
	sort.Strings(pkgs)
	want := []string{"leaf", "mid"}
	if strings.Join(pkgs, ",") != strings.Join(want, ",") {
		t.Fatalf("selected packages = %v, want %v (unrelated must be excluded)", pkgs, want)
	}

	// Strategy reporting: the import-graph strategy + a scannable label.
	if got.Strategy != StrategyImportGraph {
		t.Errorf("Strategy = %q, want %q", got.Strategy, StrategyImportGraph)
	}
	label := got.Label()
	if !strings.HasPrefix(label, string(StrategyImportGraph)) {
		t.Errorf("Label() = %q, want it to start with %q", label, StrategyImportGraph)
	}
}

// TestImportGraphExcludesUnrelatedOnLeafChange asserts the negative directly:
// changing unrelated selects ONLY unrelated, never leaf/mid (no false positives).
func TestImportGraphExcludesUnrelatedOnLeafChange(t *testing.T) {
	root := materializeFixture(t, filepath.Join("testdata", "igmod"))

	sel := NewImportGraph(nil)
	changes := []Change{{File: filepath.Join("unrelated", "unrelated.go")}}

	got, err := sel.Select(context.Background(), changes, engine.Scope{Root: root})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if !got.OK {
		t.Fatalf("expected OK selection")
	}
	pkgs := suffixes(got.Packages)
	if len(pkgs) != 1 || pkgs[0] != "unrelated" {
		t.Fatalf("selected = %v, want [unrelated] only", pkgs)
	}
}

// suffixes reduces import paths to their last path element for readable assertions.
func suffixes(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = p[strings.LastIndex(p, "/")+1:]
	}
	return out
}
