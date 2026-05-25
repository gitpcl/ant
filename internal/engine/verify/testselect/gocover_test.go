package testselect

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
)

// TestGoCoverageEndToEndSelectsOnlyCoveringTests exercises the PRODUCTION coverage
// path against the real igmod fixture with the live toolchain: it records real
// coverage (go test -coverpkg), parses it, and asserts a change to leaf.go selects
// the test packages that actually COVER that line and excludes the unrelated one.
// This proves the §5.3.1 preferred strategy works against real `go test` output,
// complementing the fake-generator unit tests.
func TestGoCoverageEndToEndSelectsOnlyCoveringTests(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; skipping live coverage test")
	}
	root := materializeFixture(t, filepath.Join("testdata", "igmod"))

	cache := NewProfileCache(NewGoProfileGenerator())
	sel := NewCoverage(cache)

	// leaf.Value() is on line 4 of leaf/leaf.go; both leaf's own test and mid's
	// test (Plus calls leaf.Value) exercise it, so both cover the line. unrelated
	// covers nothing in leaf.go and must be excluded.
	changes := []Change{{File: filepath.Join("leaf", "leaf.go"), Lines: []int{4}}}
	got, err := sel.Select(context.Background(), changes, engine.Scope{Root: root})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if !got.OK {
		t.Fatalf("expected coverage selection to apply, got OK=false (label %q)", got.Label())
	}
	if got.Strategy != StrategyCoverageMap {
		t.Errorf("Strategy = %q, want coverage-map", got.Strategy)
	}
	pkgs := suffixes(got.Packages)
	for _, p := range pkgs {
		if p == "unrelated" {
			t.Fatalf("coverage selected the unrelated package; selection must be only covering tests, got %v", pkgs)
		}
	}
	// leaf's own test definitely covers leaf.go:4.
	if !contains(pkgs, "leaf") {
		t.Fatalf("coverage selection %v must include leaf (its own test covers the changed line)", pkgs)
	}

	// Caching: a second selection over the same (unchanged) test-file set must NOT
	// regenerate the profile.
	if _, err := sel.Select(context.Background(), changes, engine.Scope{Root: root}); err != nil {
		t.Fatal(err)
	}
	if got := cache.RegenCount(); got != 1 {
		t.Fatalf("live coverage regenerated %d times across two selections, want 1 (cached)", got)
	}
}

// TestGoFingerprintChangesWithTestFiles asserts the fingerprint is stable across
// non-test edits but changes when a test file changes — the cache-invalidation
// signal behind "regenerate only when the test-file set changes".
func TestGoFingerprintChangesWithTestFiles(t *testing.T) {
	root := materializeFixture(t, filepath.Join("testdata", "igmod"))
	gen := NewGoProfileGenerator()

	fp1, err := gen.Fingerprint(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}

	// Edit a NON-test source file — fingerprint must stay stable.
	leaf := filepath.Join(root, "leaf", "leaf.go")
	src, _ := os.ReadFile(leaf)
	if err := os.WriteFile(leaf, append(src, []byte("\n// touched\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	fp2, err := gen.Fingerprint(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 != fp2 {
		t.Errorf("fingerprint changed after a NON-test edit; coverage should have been reused")
	}

	// Edit a TEST file — fingerprint must change.
	leafTest := filepath.Join(root, "leaf", "leaf_test.go")
	tsrc, _ := os.ReadFile(leafTest)
	if err := os.WriteFile(leafTest, append(tsrc, []byte("\n// touched test\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	fp3, err := gen.Fingerprint(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if fp2 == fp3 {
		t.Errorf("fingerprint did NOT change after a test-file edit; coverage would be stale")
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
