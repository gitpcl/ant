package testselect

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/gitpcl/ant/internal/engine"
)

// CoverageProfile is one test package's coverage of the module's source: for each
// covered source block it records the file, line range, and which test package
// produced the coverage. It is the parsed form of a `go test -coverprofile`
// output (mode + per-block lines). TestPkg is the package whose test run produced
// this coverage, so a changed line maps back to the test(s) that exercise it.
type CoverageProfile struct {
	// TestPkg is the import path of the test package this profile was recorded
	// from (e.g. running `go test -coverpkg=./... ./internal/foo` attributes all
	// covered blocks to ./internal/foo's tests).
	TestPkg string
	// Blocks are the covered source blocks (count>0) keyed nowhere — scanned
	// linearly when mapping a changed line, which is cheap for a single fix.
	Blocks []CoverBlock
}

// CoverBlock is one covered source region from a coverage profile: the source
// file (module-relative, e.g. "internal/foo/foo.go"), the inclusive line range it
// spans, and whether it was hit (count>0). Only hit blocks select a test.
type CoverBlock struct {
	File      string // module-relative source path
	StartLine int
	EndLine   int
	Hit       bool
}

// ProfileSet is the colony-wide cached coverage data: one profile per test
// package, plus the fingerprint of the test-file set that produced it. The
// fingerprint lets the cache regenerate ONLY when the set of test files changes —
// not per ant — which is the §5.3.1 caching requirement (generating coverage is
// expensive; doing it once per run and reusing it across every ant is the whole
// point of the cache).
type ProfileSet struct {
	Profiles    []CoverageProfile
	Fingerprint string
}

// ProfileGenerator produces the coverage ProfileSet for the module at root. It is
// injectable so the coverage-map selector is testable against a recorded profile
// (TECHSPEC §12) and so a real run records coverage from the live toolchain. The
// fingerprint identifies the current test-file set so the cache can decide whether
// the cached set is still valid.
type ProfileGenerator interface {
	// Fingerprint returns a stable hash of the module's test-file set (names +
	// mod times). Cheap relative to Generate, so the cache calls it every time to
	// decide whether to reuse or regenerate.
	Fingerprint(ctx context.Context, root string) (string, error)
	// Generate records coverage for the module, returning one profile per test
	// package. Called only when the fingerprint changed (or on first use).
	Generate(ctx context.Context, root string) (ProfileSet, error)
}

// ProfileCache caches a ProfileSet colony-wide and regenerates it ONLY when the
// test-file fingerprint changes. Every ant in a run shares one cache, so the
// expensive coverage generation happens once per distinct test-file set, not once
// per fix (TECHSPEC §5.3.1). It is safe for concurrent use: the colony runs ants
// in parallel, and a single-flight mutex ensures the profile is generated once
// even under a concurrent first miss.
type ProfileCache struct {
	gen ProfileGenerator

	mu          sync.Mutex
	have        bool
	set         ProfileSet
	regenCount  int // how many times Generate actually ran (test asserts =1 per set)
	fingerprint string
}

// NewProfileCache wraps a ProfileGenerator in a colony-wide cache. The colony
// builds ONE cache per run and hands the same instance to every coverage-map
// selector, so generation is shared.
func NewProfileCache(gen ProfileGenerator) *ProfileCache {
	return &ProfileCache{gen: gen}
}

// Get returns the cached ProfileSet, generating it on first use and regenerating
// it only when the test-file fingerprint has changed since the cached set. The
// regenerate-on-fingerprint-change rule means editing a NON-test source file
// reuses the cached coverage (the common case for a fix), while adding/removing a
// test invalidates it. Concurrent callers during a miss block on the mutex and
// then observe the freshly-generated set — Generate runs at most once per set.
func (c *ProfileCache) Get(ctx context.Context, root string) (ProfileSet, error) {
	fp, err := c.gen.Fingerprint(ctx, root)
	if err != nil {
		return ProfileSet{}, fmt.Errorf("coverage cache: fingerprint: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.have && c.fingerprint == fp {
		return c.set, nil // cache hit — reused across ants, NOT regenerated
	}

	set, err := c.gen.Generate(ctx, root)
	if err != nil {
		return ProfileSet{}, fmt.Errorf("coverage cache: generate: %w", err)
	}
	set.Fingerprint = fp
	c.set = set
	c.fingerprint = fp
	c.have = true
	c.regenCount++
	return set, nil
}

// RegenCount reports how many times the underlying generator actually ran. Tests
// assert it stays at 1 across many ants sharing one cache (the profile is cached,
// not regenerated per ant — the §5.3.1 acceptance criterion).
func (c *ProfileCache) RegenCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.regenCount
}

// coverageSelector maps changed lines to the test packages that cover them, using
// the colony-wide cached coverage profiles (TECHSPEC §5.3.1, preferred strategy).
// It selects ONLY covering tests: a test package is chosen iff one of its covered
// blocks spans a changed line in a changed file. It is strictly more precise than
// import-graph (line granularity, not package granularity).
type coverageSelector struct {
	cache *ProfileCache
}

// compile-time assertion that coverageSelector satisfies TestSelector.
var _ TestSelector = (*coverageSelector)(nil)

// NewCoverage returns the coverage-map selector backed by a shared ProfileCache.
// The colony builds one cache per run and passes it here for every ant, so the
// profile is generated once and reused (the caching contract).
func NewCoverage(cache *ProfileCache) TestSelector {
	return &coverageSelector{cache: cache}
}

// Select loads the (cached) profiles and selects every test package with a hit
// block covering a changed line. Returns OK=false when there is no coverage data,
// no changed lines, or no covering test — so the verifier falls through to
// import-graph rather than running nothing. An error from the cache (profile
// generation/parse failure) is returned so the verifier can degrade on it.
func (s *coverageSelector) Select(ctx context.Context, changes []Change, scope engine.Scope) (Selection, error) {
	if s.cache == nil || len(changes) == 0 {
		return Selection{Strategy: StrategyCoverageMap}, nil
	}
	root := scope.Root
	if root == "" {
		root = "."
	}

	set, err := s.cache.Get(ctx, root)
	if err != nil {
		return Selection{}, err
	}
	if len(set.Profiles) == 0 {
		// No coverage recorded (no tests, or coverage tooling unavailable) — fall
		// through to import-graph.
		return Selection{Strategy: StrategyCoverageMap}, nil
	}

	// Index changed lines by file for O(1) membership tests.
	changedLines := make(map[string]map[int]bool, len(changes))
	for _, ch := range changes {
		if len(ch.Lines) == 0 {
			continue // line-less change can't be coverage-mapped; import-graph handles it
		}
		set := changedLines[normPath(ch.File)]
		if set == nil {
			set = make(map[int]bool)
			changedLines[normPath(ch.File)] = set
		}
		for _, ln := range ch.Lines {
			set[ln] = true
		}
	}
	if len(changedLines) == 0 {
		return Selection{Strategy: StrategyCoverageMap}, nil
	}

	selected := make(map[string]bool)
	for _, prof := range set.Profiles {
		if profileCoversAnyChange(prof, changedLines) {
			selected[prof.TestPkg] = true
		}
	}
	if len(selected) == 0 {
		return Selection{Strategy: StrategyCoverageMap}, nil
	}

	pkgs := make([]string, 0, len(selected))
	for p := range selected {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs) // deterministic selection for stable provenance/tests

	return Selection{
		Tests:    pkgs,
		Packages: pkgs,
		Strategy: StrategyCoverageMap,
		OK:       true,
	}, nil
}

// profileCoversAnyChange reports whether any hit block in prof spans a changed
// line of the matching file. The block's [StartLine,EndLine] range is inclusive.
func profileCoversAnyChange(prof CoverageProfile, changedLines map[string]map[int]bool) bool {
	for _, b := range prof.Blocks {
		if !b.Hit {
			continue
		}
		lines, ok := changedLines[normPath(b.File)]
		if !ok {
			continue
		}
		for ln := range lines {
			if ln >= b.StartLine && ln <= b.EndLine {
				return true
			}
		}
	}
	return false
}

// normPath normalizes a path to forward slashes for cross-form comparison between
// diff paths and profile paths.
func normPath(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}

// ParseProfile parses a Go coverage profile body (the `go test -coverprofile`
// format) into CoverBlocks attributed to testPkg. The format is a `mode:` header
// line followed by `importpath/file.go:startLine.col,endLine.col numStmts count`
// lines. modulePrefix is stripped from the leading import path so block File is
// module-relative (matching the diff's relative paths). It is exported so a
// ProfileGenerator implementation (and tests) can build profiles from raw
// `go test` output.
func ParseProfile(testPkg, modulePrefix string, body []byte) (CoverageProfile, error) {
	prof := CoverageProfile{TestPkg: testPkg}
	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		blk, ok, err := parseCoverLine(line, modulePrefix)
		if err != nil {
			return CoverageProfile{}, fmt.Errorf("parse coverage line %q: %w", line, err)
		}
		if ok {
			prof.Blocks = append(prof.Blocks, blk)
		}
	}
	return prof, nil
}

// parseCoverLine parses one `path:sl.sc,el.ec nstmts count` profile line. A line
// is a hit when count>0. modulePrefix (e.g. "example.com/igmod/") is trimmed so
// File is module-relative.
func parseCoverLine(line, modulePrefix string) (CoverBlock, bool, error) {
	colon := strings.LastIndex(line, ":")
	if colon < 0 {
		return CoverBlock{}, false, fmt.Errorf("no file:range separator")
	}
	file := line[:colon]
	if modulePrefix != "" {
		file = strings.TrimPrefix(file, modulePrefix)
	}
	rest := strings.Fields(line[colon+1:])
	if len(rest) != 3 {
		return CoverBlock{}, false, fmt.Errorf("want range nstmts count, got %d fields", len(rest))
	}
	startLine, endLine, err := parseRange(rest[0])
	if err != nil {
		return CoverBlock{}, false, err
	}
	count, err := strconv.Atoi(rest[2])
	if err != nil {
		return CoverBlock{}, false, fmt.Errorf("bad count %q: %w", rest[2], err)
	}
	return CoverBlock{File: normPath(file), StartLine: startLine, EndLine: endLine, Hit: count > 0}, true, nil
}

// parseRange parses `startLine.startCol,endLine.endCol` into the line bounds; the
// columns are not needed for line-level mapping.
func parseRange(r string) (startLine, endLine int, err error) {
	comma := strings.IndexByte(r, ',')
	if comma < 0 {
		return 0, 0, fmt.Errorf("malformed range %q", r)
	}
	start, err := lineOf(r[:comma])
	if err != nil {
		return 0, 0, err
	}
	end, err := lineOf(r[comma+1:])
	if err != nil {
		return 0, 0, err
	}
	return start, end, nil
}

// lineOf extracts the line number from a `line.col` token.
func lineOf(tok string) (int, error) {
	if dot := strings.IndexByte(tok, '.'); dot >= 0 {
		tok = tok[:dot]
	}
	n, err := strconv.Atoi(tok)
	if err != nil {
		return 0, fmt.Errorf("bad line %q: %w", tok, err)
	}
	return n, nil
}
