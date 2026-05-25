package verify

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/verify/testselect"
)

// CheckTestsAffected is the canonical name of the tests:affected check, recorded
// in its CheckResult so review/--json show which gate fired and via which strategy.
const CheckTestsAffected = "tests:affected"

// TestRunner runs the selected tests in dir and returns combined output plus an
// error on failure. It is injectable so the tests:affected verifier's
// selection + scratch-tree + reporting logic is testable WITHOUT the ant repo
// running its own suite (TECHSPEC §12). Production uses goTestRunner, which runs
// EXACTLY the selected package patterns (never ./...).
type TestRunner func(ctx context.Context, dir string, packages, runArgs []string) (output []byte, err error)

// affectedVerifier implements tests:affected (TECHSPEC §5.3.1): it applies the
// diff to a SCRATCH COPY, picks the smallest trustworthy set of tests via the
// TestSelector strategies in priority order (coverage-map → import-graph →
// package-fallback), runs ONLY those tests in the scratch copy, and reports WHICH
// strategy it used so the developer sees precise-vs-coarse. It NEVER degrades to
// the whole suite — the last resort is the package/dir-scoped package-fallback,
// which always yields a scoped selection.
//
// It does NOT lock: the colony serializes build-state verifiers behind the pool's
// per-project mutex (TECHSPEC §8.1), so locking here would be redundant.
type affectedVerifier struct {
	selectors []testselect.TestSelector // in priority order
	run       TestRunner
}

// compile-time assertion that affectedVerifier satisfies engine.Verifier.
var _ engine.Verifier = (*affectedVerifier)(nil)

// AffectedConfig wires the tests:affected verifier. Cache is the colony-wide
// coverage cache (one per run, shared across ants so the profile is generated
// once — TECHSPEC §5.3.1); a nil Cache disables the coverage-map strategy and the
// verifier starts at import-graph. ListCmd backs the import-graph selector (nil →
// live `go list`). Run executes the selected tests (nil → live `go test`). All are
// injectable so the verifier is fully hermetic in tests.
type AffectedConfig struct {
	Cache   *testselect.ProfileCache
	ListCmd testselect.ListCommand
	Run     TestRunner
}

// NewTestsAffected builds the tests:affected verifier with its three strategies in
// priority order. With a nil cache the coverage-map strategy is omitted (the
// verifier begins at import-graph); a non-nil cache puts coverage-map first.
func NewTestsAffected(cfg AffectedConfig) engine.Verifier {
	run := cfg.Run
	if run == nil {
		run = goTestRunner
	}
	var selectors []testselect.TestSelector
	if cfg.Cache != nil {
		selectors = append(selectors, testselect.NewCoverage(cfg.Cache))
	}
	selectors = append(selectors,
		testselect.NewImportGraph(cfg.ListCmd),
		testselect.NewPackageFallback(),
	)
	return &affectedVerifier{selectors: selectors, run: run}
}

// NewTestsAffectedWith is the seam tests use to inject an explicit, ordered set of
// selectors and a runner — so degradation through all three strategies can be
// driven deterministically without a live toolchain.
func NewTestsAffectedWith(run TestRunner, selectors ...testselect.TestSelector) engine.Verifier {
	if run == nil {
		run = goTestRunner
	}
	return &affectedVerifier{selectors: selectors, run: run}
}

// Verify applies the diff to a scratch copy, selects the affected tests via the
// first strategy that yields a usable selection, and runs ONLY those tests in the
// copy. Pass = the selected tests pass; the CheckResult.Detail is the strategy
// label (e.g. "coverage-map (7 tests)") so provenance shows how thoroughly the
// fix was checked. Fail carries the test output as the reason. A scratch-prep or
// selection error is a failed check (a visible skip), never a panic.
func (v *affectedVerifier) Verify(ctx context.Context, diff engine.ProposedDiff, scope engine.Scope) engine.VerifyResult {
	changes := changesFromDiff(diff)
	if len(changes) == 0 {
		// Nothing changed — there is nothing to verify against. Pass with a clear
		// detail rather than fabricate a test run.
		return passResult(CheckTestsAffected, "no changed files to select tests for")
	}

	st, cleanup, err := newScratchTree(scope.Root, diff)
	if err != nil {
		return failResult(CheckTestsAffected, fmt.Sprintf("could not prepare scratch tree: %v", err))
	}
	defer cleanup()

	// The selectors analyze the POST-FIX tree (the scratch copy): coverage is
	// recorded there and the import graph reflects the patched imports.
	scratchScope := scope
	scratchScope.Root = st.root

	sel, err := v.choose(ctx, changes, scratchScope)
	if err != nil {
		return failResult(CheckTestsAffected, fmt.Sprintf("selecting affected tests failed: %v", err))
	}
	if !sel.OK {
		// Every strategy declined — there are no tests to run for this change. That
		// is a PASS (a doc-only or test-less change has no affected tests), reported
		// honestly so it is not mistaken for a precise check.
		return passResult(CheckTestsAffected, "tests:affected (no affected tests)")
	}

	out, err := v.run(ctx, st.root, sel.Packages, sel.RunArgs)
	if err != nil {
		detail := fmt.Sprintf("%s: tests failed: %v", sel.Label(), err)
		if len(bytes.TrimSpace(out)) > 0 {
			detail = fmt.Sprintf("%s: tests failed: %s", sel.Label(), bytes.TrimSpace(out))
		}
		return failResult(CheckTestsAffected, detail)
	}

	// PASS detail is the bare, scannable strategy label so review provenance
	// surfaces it (review.isStrategyDetail requires <=24 chars, no "."/newline).
	return passResult(CheckTestsAffected, sel.Label())
}

// choose tries each selector in priority order and returns the first usable
// (OK=true) selection. A selector that returns an error stops the chain only if it
// is the coverage strategy failing on a real profile error — but to keep
// degradation graceful, a selector error is treated as "this strategy is
// unavailable, try the next" UNLESS every strategy errors, in which case the last
// error is returned. This guarantees the verifier never gets stuck: package-
// fallback always yields a scoped selection for any non-empty change set.
func (v *affectedVerifier) choose(ctx context.Context, changes []testselect.Change, scope engine.Scope) (testselect.Selection, error) {
	var lastErr error
	for _, s := range v.selectors {
		sel, err := s.Select(ctx, changes, scope)
		if err != nil {
			lastErr = err
			continue // strategy unavailable — degrade to the next
		}
		if sel.OK {
			return sel, nil
		}
	}
	// No strategy produced a usable selection. If the only reason was errors,
	// surface the last one; otherwise it is a clean "no affected tests".
	if lastErr != nil {
		return testselect.Selection{}, lastErr
	}
	return testselect.Selection{}, nil
}

// goTestRunner is the production TestRunner: `go test <runArgs...> <packages...>`
// in dir. It runs EXACTLY the selected package patterns — never "./..." — so the
// §5.3.1 "never the whole suite" rule is enforced at the runner boundary, not just
// the selector. Combined stdout+stderr is returned so a failure reaches the
// CheckResult detail verbatim. -count=1 disables the test cache so the post-fix
// scratch tree is actually exercised.
func goTestRunner(ctx context.Context, dir string, packages, runArgs []string) ([]byte, error) {
	if len(packages) == 0 {
		return nil, fmt.Errorf("tests:affected: refusing to run with no selected packages (would imply the whole suite)")
	}
	args := []string{"test", "-count=1"}
	args = append(args, runArgs...)
	args = append(args, packages...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.Bytes(), err
}
