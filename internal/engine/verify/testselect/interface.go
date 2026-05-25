// Package testselect implements smart test selection for the tests:affected
// verifier (TECHSPEC §5.3.1): given the files a fix changed, pick the SMALLEST
// trustworthy set of tests to run, and ALWAYS report which strategy was used so a
// developer sees whether the fix was checked precisely or coarsely.
//
// Three selectors implement the per-language TestSelector interface and are tried
// in priority order by the tests:affected verifier:
//
//  1. coverage-map     — map changed lines to the tests that cover them (preferred,
//     most precise). Requires a coverage profile.
//  2. import-graph     — select every test package that transitively imports a
//     changed file (fallback when no coverage data exists).
//  3. package-fallback — run the tests in the changed file's own package/dir
//     (last resort, coarse but still SCOPED — never the whole suite).
//
// The hard rule (TECHSPEC §5.3.1): the last resort is package/dir-scoped, NEVER a
// full-suite run. A full-suite fallback would collapse confidence — the developer
// could no longer tell precise from coarse. Every Selection therefore carries its
// Strategy and a short Label() that surfaces in CheckResult.Detail and review
// provenance.
package testselect

import (
	"context"

	"github.com/gitpcl/ant/internal/engine"
)

// Strategy identifies which selection method produced a Selection. It is the
// trust signal the developer reads in review provenance: precise (coverage-map)
// vs coarse (package-fallback).
type Strategy string

const (
	// StrategyCoverageMap is the preferred, most precise strategy: changed lines
	// mapped to the exact tests that cover them via a coverage profile.
	StrategyCoverageMap Strategy = "coverage-map"
	// StrategyImportGraph selects every test package that transitively imports a
	// changed file. Used when no coverage profile is available.
	StrategyImportGraph Strategy = "import-graph"
	// StrategyPackageFallback runs the tests in the changed file's own package/dir.
	// The last resort — coarse, but still scoped, NEVER the whole suite (§5.3.1).
	StrategyPackageFallback Strategy = "package-fallback"
)

// Change is one changed file from the diff, with the line numbers the fix
// touched (1-based, added or removed). A selector that maps lines (coverage-map)
// uses Lines; selectors that work at file granularity (import-graph,
// package-fallback) need only File. Lines may be empty when the caller could not
// extract them — a line-based selector must then degrade rather than panic.
type Change struct {
	File  string // path relative to the scope root
	Lines []int  // 1-based changed line numbers within File (may be empty)
}

// Selection is a selector's output: the tests to run, the strategy that chose
// them, and whether the selector could produce a usable answer at all. A selector
// that cannot apply (e.g. coverage-map with no profile) returns OK=false so the
// verifier moves to the next strategy in priority order; it does NOT fabricate an
// empty selection that would silently skip all tests.
type Selection struct {
	// Tests are the selected test identifiers in a form the runner understands.
	// For Go these are package import-path patterns (e.g. "./internal/foo") or
	// "-run" name patterns, interpreted by the runner the verifier injects.
	Tests []string
	// RunArgs are extra `go test` arguments narrowing the run to exactly the
	// selected tests (e.g. ["-run", "TestA|TestB"]). Empty means "run the named
	// packages' tests in full".
	RunArgs []string
	// Strategy is which method produced this selection (for provenance).
	Strategy Strategy
	// Packages are the package patterns whose tests should run (the runner's
	// positional args). Always scoped — never "./..." for package-fallback.
	Packages []string
	// OK reports whether this selector could produce a usable selection. False
	// means "I do not apply; try the next strategy" — never "run nothing".
	OK bool
}

// Label is the short, scannable strategy label that goes into CheckResult.Detail
// and surfaces in `ant review` provenance. It MUST stay <=24 chars and contain no
// "." or newline, because review.isStrategyDetail only appends details meeting
// that bound (review/view.go). Examples: "coverage-map (7 tests)",
// "import-graph (3 tests)", "package-fallback (2)". Tests is the count the
// developer cares about — how thoroughly the fix was checked.
func (s Selection) Label() string {
	n := len(s.Tests)
	switch s.Strategy {
	case StrategyPackageFallback:
		// Coarse: report package count, prefixed so the developer sees it's a
		// last-resort scope, not a precise selection.
		return string(s.Strategy) + " (" + itoa(len(s.Packages)) + ")"
	default:
		return string(s.Strategy) + " (" + itoa(n) + " tests)"
	}
}

// TestSelector picks the tests affected by a set of changes within a scope. It is
// per-language by construction: the Go selectors live here; a future JS/TS
// selector implements the same interface in its own file, so languages are added
// independently (TECHSPEC §5.3.1) without touching the verifier.
//
// Select returns OK=false (not an error) when the selector simply does not apply
// to this run, so the verifier can fall through to the next strategy. An error is
// reserved for an actual failure (e.g. a malformed coverage profile) the caller
// should surface.
type TestSelector interface {
	Select(ctx context.Context, changes []Change, scope engine.Scope) (Selection, error)
}

// itoa is a tiny strconv.Itoa wrapper kept local so the Label hot path has no
// import beyond what the package already needs; it exists only to keep Label
// allocation-light and dependency-free.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
