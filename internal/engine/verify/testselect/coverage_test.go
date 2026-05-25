package testselect

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
)

// fakeGenerator is a hermetic ProfileGenerator: it returns a fixed ProfileSet and
// counts how many times Generate actually ran, so a test can assert the cache
// regenerates exactly once across many ants (the §5.3.1 caching contract). The
// fingerprint is settable so a test can simulate a changed test-file set.
type fakeGenerator struct {
	fingerprint atomic.Value // string
	profiles    []CoverageProfile
	genCalls    int32
}

func (f *fakeGenerator) Fingerprint(_ context.Context, _ string) (string, error) {
	if v, ok := f.fingerprint.Load().(string); ok {
		return v, nil
	}
	return "fp-0", nil
}

func (f *fakeGenerator) Generate(_ context.Context, _ string) (ProfileSet, error) {
	atomic.AddInt32(&f.genCalls, 1)
	return ProfileSet{Profiles: f.profiles}, nil
}

// recordedProfiles is a hand-authored coverage fixture: the "covering" test
// package covers internal/foo/foo.go lines 3-5; the "unrelated" test package
// covers internal/bar/bar.go lines 2-9 only. A change to foo.go:4 must select
// ONLY the covering package.
func recordedProfiles() []CoverageProfile {
	return []CoverageProfile{
		{
			TestPkg: "example.com/m/internal/foo",
			Blocks: []CoverBlock{
				{File: "internal/foo/foo.go", StartLine: 3, EndLine: 5, Hit: true},
			},
		},
		{
			TestPkg: "example.com/m/internal/bar",
			Blocks: []CoverBlock{
				{File: "internal/bar/bar.go", StartLine: 2, EndLine: 9, Hit: true},
				// A NON-hit block over foo.go must NOT cause selection.
				{File: "internal/foo/foo.go", StartLine: 4, EndLine: 4, Hit: false},
			},
		},
	}
}

// TestCoverageSelectsOnlyCoveringTests asserts a changed line maps to ONLY the
// test package that actually covers it (a hit block spanning the line) — the bar
// package, whose only foo.go block is a MISS, is excluded.
func TestCoverageSelectsOnlyCoveringTests(t *testing.T) {
	cache := NewProfileCache(&fakeGenerator{profiles: recordedProfiles()})
	sel := NewCoverage(cache)

	changes := []Change{{File: "internal/foo/foo.go", Lines: []int{4}}}
	got, err := sel.Select(context.Background(), changes, engine.Scope{Root: "."})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if !got.OK {
		t.Fatalf("expected OK selection")
	}
	if got.Strategy != StrategyCoverageMap {
		t.Errorf("Strategy = %q, want coverage-map", got.Strategy)
	}
	if len(got.Packages) != 1 || got.Packages[0] != "example.com/m/internal/foo" {
		t.Fatalf("selected = %v, want [example.com/m/internal/foo] only (bar's block is a MISS)", got.Packages)
	}
	if got.Label() != "coverage-map (1 tests)" {
		t.Errorf("Label() = %q, want %q", got.Label(), "coverage-map (1 tests)")
	}
}

// TestCoverageSelectsNothingForUncoveredLine asserts a changed line covered by NO
// hit block yields OK=false, so the verifier falls through to import-graph rather
// than running nothing.
func TestCoverageSelectsNothingForUncoveredLine(t *testing.T) {
	cache := NewProfileCache(&fakeGenerator{profiles: recordedProfiles()})
	sel := NewCoverage(cache)

	changes := []Change{{File: "internal/foo/foo.go", Lines: []int{99}}}
	got, err := sel.Select(context.Background(), changes, engine.Scope{Root: "."})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.OK {
		t.Fatalf("expected OK=false for a line no test covers, got selection %v", got.Packages)
	}
}

// TestCoverageProfileCachedAcrossAnts is the §5.3.1 caching acceptance criterion:
// many ants in one run share one cache, and the expensive profile generation runs
// EXACTLY ONCE while the test-file fingerprint is stable — not once per ant.
func TestCoverageProfileCachedAcrossAnts(t *testing.T) {
	gen := &fakeGenerator{profiles: recordedProfiles()}
	cache := NewProfileCache(gen)
	sel := NewCoverage(cache)

	changes := []Change{{File: "internal/foo/foo.go", Lines: []int{4}}}

	// Simulate 50 concurrent ants all selecting against the same cache.
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := sel.Select(context.Background(), changes, engine.Scope{Root: "."}); err != nil {
				t.Errorf("Select: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := cache.RegenCount(); got != 1 {
		t.Fatalf("profile generated %d times across 50 ants, want 1 (cached, not regenerated per ant)", got)
	}
	if got := atomic.LoadInt32(&gen.genCalls); got != 1 {
		t.Fatalf("generator ran %d times, want 1", got)
	}
}

// TestCoverageRegeneratesWhenTestFilesChange asserts the cache regenerates ONLY
// when the test-file fingerprint changes (not for every call): a stable
// fingerprint reuses the set; a changed fingerprint triggers exactly one more
// generation.
func TestCoverageRegeneratesWhenTestFilesChange(t *testing.T) {
	gen := &fakeGenerator{profiles: recordedProfiles()}
	gen.fingerprint.Store("fp-A")
	cache := NewProfileCache(gen)
	sel := NewCoverage(cache)
	changes := []Change{{File: "internal/foo/foo.go", Lines: []int{4}}}

	for i := 0; i < 3; i++ {
		if _, err := sel.Select(context.Background(), changes, engine.Scope{Root: "."}); err != nil {
			t.Fatal(err)
		}
	}
	if got := cache.RegenCount(); got != 1 {
		t.Fatalf("stable fingerprint regenerated %d times, want 1", got)
	}

	// The test-file set changes → fingerprint changes → exactly one regeneration.
	gen.fingerprint.Store("fp-B")
	if _, err := sel.Select(context.Background(), changes, engine.Scope{Root: "."}); err != nil {
		t.Fatal(err)
	}
	if got := cache.RegenCount(); got != 2 {
		t.Fatalf("after test-file change, regenerated %d times total, want 2", got)
	}
}

// TestParseProfileMapsHitsAndMisses asserts the profile parser handles the real
// `go test -coverprofile` line format, trims the module prefix to module-relative
// paths, and classifies count>0 as hit and count==0 as miss.
func TestParseProfileMapsHitsAndMisses(t *testing.T) {
	body := []byte("mode: set\n" +
		"example.com/m/internal/foo/foo.go:3.16,5.4 2 1\n" +
		"example.com/m/internal/foo/foo.go:7.2,7.20 1 0\n")
	prof, err := ParseProfile("example.com/m/internal/foo", "example.com/m/", body)
	if err != nil {
		t.Fatalf("ParseProfile: %v", err)
	}
	if len(prof.Blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(prof.Blocks))
	}
	b0 := prof.Blocks[0]
	if b0.File != "internal/foo/foo.go" || b0.StartLine != 3 || b0.EndLine != 5 || !b0.Hit {
		t.Errorf("block0 = %+v, want internal/foo/foo.go 3-5 hit", b0)
	}
	if prof.Blocks[1].Hit {
		t.Errorf("block1 should be a miss (count 0)")
	}
}
