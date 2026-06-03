package verify

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/langmap"
	"github.com/gitpcl/ant/internal/engine/verify/testselect"
)

// CheckTestsAffected is the canonical name of the tests:affected check, recorded
// in its CheckResult so review/--json show which gate fired and via which strategy.
const CheckTestsAffected = "tests:affected"

// TestRunner runs the selected tests in dir and returns combined output plus an
// error on failure. It is injectable so the tests:affected verifier's
// selection + scratch-tree + reporting logic is testable WITHOUT the ant repo
// running its own suite (TECHSPEC §12). Each language registers one TestRunner in
// the per-language table; Go production uses goTestRunner, which runs EXACTLY the
// selected package patterns (never ./...).
type TestRunner func(ctx context.Context, dir string, packages, runArgs []string) (output []byte, err error)

// langRunner bundles the per-language test selection strategy set and the runner
// that executes the chosen tests. The verifier resolves the diff's language and
// dispatches to the matching langRunner; a language with no entry is an honest
// skip (the Sprint 026 "never a vacuous pass" rule applied to tests:affected).
type langRunner struct {
	selectors []testselect.TestSelector // in priority order
	run       TestRunner
}

// affectedVerifier implements tests:affected (TECHSPEC §5.3.1) per language: it
// applies the diff to a SCRATCH COPY, resolves the diff's language(s), picks the
// smallest trustworthy set of tests via that language's TestSelector strategies
// (Go: coverage-map → import-graph → package-fallback; ts/py/php: co-located),
// runs ONLY those tests in the scratch copy, and reports WHICH strategy it used.
// It NEVER degrades to the whole suite. A language with no registered runner is an
// honest skip-with-reason, never a silent pass.
//
// SECURITY (Sprint 026 audit): a test RUNNER executes REPO-CONTROLLED code by
// design — pytest imports conftest.py at collection, vitest evaluates
// vitest.config.ts, phpunit runs the bootstrap + test classes, go test compiles
// and runs the repo's _test.go. That is the SAME scan/exec trust surface the
// command: verifier is gated on (species.ScriptExecAllowed). execAllowed carries
// that decision: when false (an untrusted, never-reviewed user species) the
// verifier MUST NOT run any repo test code — it emits an honest skip-with-trust-
// reason instead, exactly like blockedVerifier does for command: scripts. The
// per-language COMPILE builders (php -l, py_compile, tsc --noEmit) are parse/
// typecheck-only and stay ungated; only the test-execution surface is gated here.
//
// It does NOT lock: the colony serializes build-state verifiers behind the pool's
// per-project mutex (TECHSPEC §8.1), so locking here would be redundant.
type affectedVerifier struct {
	table       map[string]langRunner
	execAllowed bool
}

// compile-time assertion that affectedVerifier satisfies engine.Verifier.
var _ engine.Verifier = (*affectedVerifier)(nil)

// AffectedConfig wires the tests:affected verifier. Cache is the colony-wide
// coverage cache (one per run, shared across ants so the profile is generated
// once — TECHSPEC §5.3.1); a nil Cache disables the coverage-map strategy and the
// Go path starts at import-graph. ListCmd backs the import-graph selector (nil →
// live `go list`). Run executes the selected GO tests (nil → live `go test`). The
// non-Go language runners default to their live CLI (vitest/pytest/phpunit) and
// are missing-binary tolerant. All are injectable so the verifier is hermetic.
type AffectedConfig struct {
	Cache   *testselect.ProfileCache
	ListCmd testselect.ListCommand
	Run     TestRunner

	// ExecAllowed is the scan/exec TRUST GATE (species.ScriptExecAllowed, computed
	// by species.ResolveTrust). A test runner executes repo-controlled code
	// (conftest.py, vitest.config.ts, phpunit bootstrap, _test.go), so an untrusted
	// (OriginUser, never-reviewed) species must NOT trigger it via tests:affected.
	// When false the verifier emits an honest skip-with-reason and runs NO repo
	// code. Built-in/vetted species and reviewed user species pass true, preserving
	// today's behavior. The default false is the SAFE default for an under-specified
	// config — a tests:affected verifier built without an explicit decision will
	// skip rather than execute untrusted repo code.
	ExecAllowed bool
}

// NewTestsAffected builds the per-language tests:affected verifier. The Go entry
// preserves the prior behavior exactly (three strategies in priority order, the
// injected or live `go test` runner). The ts/php/python entries add the
// co-located selector + their CLI runner. A diff whose language has no entry is an
// honest skip.
func NewTestsAffected(cfg AffectedConfig) engine.Verifier {
	goRun := cfg.Run
	if goRun == nil {
		goRun = goTestRunner
	}
	var goSelectors []testselect.TestSelector
	if cfg.Cache != nil {
		goSelectors = append(goSelectors, testselect.NewCoverage(cfg.Cache))
	}
	goSelectors = append(goSelectors,
		testselect.NewImportGraph(cfg.ListCmd),
		testselect.NewPackageFallback(),
	)

	return &affectedVerifier{execAllowed: cfg.ExecAllowed, table: map[string]langRunner{
		langmap.Go: {selectors: goSelectors, run: goRun},
		langmap.TypeScript: {
			selectors: []testselect.TestSelector{testselect.NewVitest()},
			run:       cliTestRunner("vitest", "run"),
		},
		langmap.JavaScript: {
			selectors: []testselect.TestSelector{testselect.NewVitestJS()},
			run:       cliTestRunner("vitest", "run"),
		},
		langmap.Python: {
			selectors: []testselect.TestSelector{testselect.NewPytest()},
			run:       cliTestRunner("pytest"),
		},
		langmap.PHP: {
			selectors: []testselect.TestSelector{testselect.NewPHPUnit()},
			run:       cliTestRunner("phpunit"),
		},
	}}
}

// NewTestsAffectedWith is the seam tests use to inject, for the GO language, an
// explicit ordered set of selectors and a runner — so degradation through the Go
// strategies can be driven deterministically without a live toolchain. It builds
// a Go-only verifier (its callers only drive Go fixtures).
func NewTestsAffectedWith(run TestRunner, selectors ...testselect.TestSelector) engine.Verifier {
	if run == nil {
		run = goTestRunner
	}
	return &affectedVerifier{execAllowed: true, table: map[string]langRunner{
		langmap.Go: {selectors: selectors, run: run},
	}}
}

// NewTestsAffectedForLang is the seam tests use to inject an explicit selector
// set + runner for a SPECIFIC language, so the per-language dispatch (ts→vitest,
// py→pytest, php→phpunit) can be driven hermetically with fakes.
func NewTestsAffectedForLang(language string, run TestRunner, selectors ...testselect.TestSelector) engine.Verifier {
	return &affectedVerifier{execAllowed: true, table: map[string]langRunner{
		language: {selectors: selectors, run: run},
	}}
}

// NewTestsAffectedForLangGated mirrors NewTestsAffectedForLang but lets a test
// drive the exec TRUST GATE explicitly, so the Sprint-026 regression can prove an
// untrusted (execAllowed=false) species' tests:affected runs NO repo code.
func NewTestsAffectedForLangGated(language string, execAllowed bool, run TestRunner, selectors ...testselect.TestSelector) engine.Verifier {
	return &affectedVerifier{execAllowed: execAllowed, table: map[string]langRunner{
		language: {selectors: selectors, run: run},
	}}
}

// Verify applies the diff to a scratch copy, resolves the diff's language(s), and
// runs ONLY the tests each language's first usable strategy selects. Pass = the
// selected tests pass; the CheckResult.Detail is the strategy label. A language
// with no registered runner, or whose runner binary is absent, is an honest
// skip-with-reason — never a vacuous pass (Sprint 026). A scratch-prep or
// selection error is a failed check (a visible skip), never a panic.
func (v *affectedVerifier) Verify(ctx context.Context, diff engine.ProposedDiff, scope engine.Scope) engine.VerifyResult {
	changes := changesFromDiff(diff)
	if len(changes) == 0 {
		return passResult(CheckTestsAffected, "no changed files to select tests for")
	}

	langs := diffLanguages(diff)
	var supported []string
	var unsupported []string
	for _, l := range langs {
		if _, ok := v.table[l]; ok {
			supported = append(supported, l)
		} else {
			unsupported = append(unsupported, l)
		}
	}
	if len(supported) == 0 {
		return skipResult(CheckTestsAffected, fmt.Sprintf("no test runner for %s", strings.Join(langs, ", ")))
	}

	// SECURITY GATE (Sprint 026 audit): running the selected tests executes
	// REPO-CONTROLLED code (conftest.py / vitest.config.ts / phpunit bootstrap /
	// _test.go). An untrusted species must not trigger that, exactly as it cannot
	// run a command: verifier script. Skip with a trust reason — never run repo
	// code, never a vacuous pass. This is checked BEFORE the scratch tree is even
	// prepared, so a denied species touches nothing.
	if !v.execAllowed {
		return skipResult(CheckTestsAffected, fmt.Sprintf(
			"tests:affected runs repo-controlled test code for %s and this species is not yet trusted to execute it (review it once with `ant review` to allow exec)",
			strings.Join(supported, ", ")))
	}

	st, cleanup, err := newScratchTree(scope.Root, diff)
	if err != nil {
		return failResult(CheckTestsAffected, fmt.Sprintf("could not prepare scratch tree: %v", err))
	}
	defer cleanup()

	// The selectors analyze the POST-FIX tree (the scratch copy).
	scratchScope := scope
	scratchScope.Root = st.root

	var ranAny bool
	var labels []string
	for _, l := range supported {
		lr := v.table[l]
		langChanges := changesForLanguage(changes, l)
		if len(langChanges) == 0 {
			continue
		}
		sel, err := choose(ctx, lr.selectors, langChanges, scratchScope)
		if err != nil {
			return failResult(CheckTestsAffected, fmt.Sprintf("selecting affected tests failed: %v", err))
		}
		if !sel.OK {
			continue // no affected tests for this language's changes
		}
		out, err := lr.run(ctx, st.root, sel.Packages, sel.RunArgs)
		if err != nil {
			if isBinaryNotFound(err) {
				// Missing test toolchain → clean skip for this language (CI without
				// vitest/pytest/phpunit stays green — Sprint 026 env note).
				labels = append(labels, fmt.Sprintf("%s skipped (no runner binary)", l))
				continue
			}
			detail := fmt.Sprintf("%s: tests failed: %v", sel.Label(), err)
			if len(bytes.TrimSpace(out)) > 0 {
				detail = fmt.Sprintf("%s: tests failed: %s", sel.Label(), bytes.TrimSpace(out))
			}
			return failResult(CheckTestsAffected, detail)
		}
		ranAny = true
		labels = append(labels, sel.Label())
	}

	if !ranAny {
		// No language produced a usable selection that actually ran.
		if len(unsupported) > 0 {
			return skipResult(CheckTestsAffected, fmt.Sprintf("no test runner for %s", strings.Join(unsupported, ", ")))
		}
		if len(labels) > 0 {
			// Every selected language's runner binary was absent — honest skip.
			return skipResult(CheckTestsAffected, strings.Join(labels, "; "))
		}
		return passResult(CheckTestsAffected, "tests:affected (no affected tests)")
	}

	// PASS detail is the strategy label(s) so review provenance shows precision.
	return passResult(CheckTestsAffected, strings.Join(labels, "; "))
}

// changesForLanguage filters the change set to files of the given language, so a
// language's selectors only see its own changed files.
func changesForLanguage(changes []testselect.Change, language string) []testselect.Change {
	var out []testselect.Change
	for _, c := range changes {
		if langmap.LanguageForPath(c.File) == language {
			out = append(out, c)
		}
	}
	return out
}

// choose tries each selector in priority order and returns the first usable
// (OK=true) selection. A selector error is treated as "this strategy is
// unavailable, try the next" UNLESS every strategy errors, in which case the last
// error is returned. This guarantees the verifier never gets stuck.
func choose(ctx context.Context, selectors []testselect.TestSelector, changes []testselect.Change, scope engine.Scope) (testselect.Selection, error) {
	var lastErr error
	for _, s := range selectors {
		sel, err := s.Select(ctx, changes, scope)
		if err != nil {
			lastErr = err
			continue
		}
		if sel.OK {
			return sel, nil
		}
	}
	if lastErr != nil {
		return testselect.Selection{}, lastErr
	}
	return testselect.Selection{}, nil
}

// goTestRunner is the production Go TestRunner: `go test <runArgs...>
// <packages...>` in dir. It runs EXACTLY the selected package patterns — never
// "./..." — so the §5.3.1 "never the whole suite" rule is enforced at the runner
// boundary. Combined stdout+stderr is returned so a failure reaches the
// CheckResult detail verbatim. -count=1 disables the test cache.
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

// cliTestRunner returns a TestRunner that runs `binary preArgs… <packages…>
// <runArgs…>` in dir — the production runner for the non-Go language waves
// (vitest run, pytest, phpunit). The selected co-located test FILES are passed as
// positional args, so the run is scoped to exactly those, never the whole suite.
// A missing binary surfaces as an exec-not-found error the verifier converts to a
// clean skip (CI without the toolchain stays green).
func cliTestRunner(binary string, preArgs ...string) TestRunner {
	return func(ctx context.Context, dir string, packages, runArgs []string) ([]byte, error) {
		if len(packages) == 0 {
			return nil, fmt.Errorf("tests:affected: refusing to run %s with no selected tests (would imply the whole suite)", binary)
		}
		args := append([]string{}, preArgs...)
		args = append(args, packages...)
		args = append(args, runArgs...)
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = dir
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err := cmd.Run()
		return buf.Bytes(), err
	}
}
