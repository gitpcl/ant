package verify_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/verify"
	"github.com/gitpcl/ant/internal/engine/verify/testselect"
)

// stubSelector is a hermetic TestSelector that returns a fixed Selection (or
// declines) so the verifier's degradation order can be driven deterministically.
type stubSelector struct {
	sel testselect.Selection
	err error
}

func (s stubSelector) Select(context.Context, []testselect.Change, engine.Scope) (testselect.Selection, error) {
	return s.sel, s.err
}

// recordingRunner captures the package args the verifier asked to run, so a test
// can assert ONLY the selected (scoped) packages ran — never "./...".
type recordingRunner struct {
	gotPackages []string
	gotRunArgs  []string
	fail        bool
}

func (r *recordingRunner) run(_ context.Context, _ string, packages, runArgs []string) ([]byte, error) {
	r.gotPackages = append([]string(nil), packages...)
	r.gotRunArgs = append([]string(nil), runArgs...)
	if r.fail {
		return []byte("--- FAIL: TestX"), fmt.Errorf("exit status 1")
	}
	return []byte("ok"), nil
}

// a minimal one-package Go module so the scratch-tree apply has a real file to
// patch (the selection/run is stubbed, so no toolchain is needed).
func affectedFixture() (string, string) {
	const old = "package main\n\nfunc main() {\n\tprintln(\"a\")\n}\n"
	const new = "package main\n\nfunc main() {\n\tprintln(\"b\")\n}\n"
	return old, new
}

// TestAffectedReportsCoverageMapStrategy: when the first (coverage) selector
// yields a selection, the verifier runs exactly its packages and the CheckResult
// detail is the coverage-map label (so review provenance shows a PRECISE check).
func TestTestsAffectedReportsCoverageMapStrategy(t *testing.T) {
	root := t.TempDir()
	old, newBody := affectedFixture()
	writeModule(t, root, old)

	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "main.go", Patch: replaceFilePatch("main.go", old, newBody)}},
		Fixer: "test",
	}

	runner := &recordingRunner{}
	v := verify.NewTestsAffectedWith(runner.run,
		stubSelector{sel: testselect.Selection{
			Packages: []string{"./pkg/a"}, Tests: []string{"./pkg/a"},
			Strategy: testselect.StrategyCoverageMap, OK: true,
		}},
		stubSelector{sel: testselect.Selection{Strategy: testselect.StrategyImportGraph}},
		testselect.NewPackageFallback(),
	)

	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if !res.Passed {
		t.Fatalf("expected pass, got %+v", res)
	}
	if got := res.Checks[0].Name; got != verify.CheckTestsAffected {
		t.Errorf("check name = %q, want %q", got, verify.CheckTestsAffected)
	}
	if got := res.Checks[0].Detail; got != "coverage-map (1 tests)" {
		t.Errorf("detail = %q, want the coverage-map strategy label", got)
	}
	if len(runner.gotPackages) != 1 || runner.gotPackages[0] != "./pkg/a" {
		t.Fatalf("ran packages %v, want only the coverage-selected [./pkg/a]", runner.gotPackages)
	}
	for _, p := range runner.gotPackages {
		if p == "./..." || strings.HasSuffix(p, "/...") {
			t.Fatalf("verifier ran the whole suite (%q) — forbidden by §5.3.1", p)
		}
	}
}

// TestAffectedDegradesThroughAllThreeStrategies drives the full priority order:
// coverage declines, import-graph declines, package-fallback wins — and the
// reported strategy is package-fallback (so the developer sees the check was
// COARSE). It also proves the fallback is package-scoped, not the whole suite.
func TestTestsAffectedDegradesThroughAllThreeStrategies(t *testing.T) {
	root := t.TempDir()
	old, newBody := affectedFixture()
	writeModule(t, root, old)

	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "main.go", Patch: replaceFilePatch("main.go", old, newBody)}},
		Fixer: "test",
	}

	runner := &recordingRunner{}
	// coverage declines (OK=false), import-graph declines, real package-fallback wins.
	v := verify.NewTestsAffectedWith(runner.run,
		stubSelector{sel: testselect.Selection{Strategy: testselect.StrategyCoverageMap}},
		stubSelector{sel: testselect.Selection{Strategy: testselect.StrategyImportGraph}},
		testselect.NewPackageFallback(),
	)

	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if !res.Passed {
		t.Fatalf("expected pass via fallback, got %+v", res)
	}
	detail := res.Checks[0].Detail
	if !strings.HasPrefix(detail, "package-fallback") {
		t.Fatalf("detail = %q, want the package-fallback (coarse) label", detail)
	}
	if len(runner.gotPackages) == 0 {
		t.Fatal("fallback must run the changed package's tests")
	}
	for _, p := range runner.gotPackages {
		if p == "./..." || strings.HasSuffix(p, "/...") {
			t.Fatalf("fallback ran the whole suite (%q) — forbidden by §5.3.1", p)
		}
	}
	// main.go lives at the module root → fallback scopes to "." only.
	if len(runner.gotPackages) != 1 || runner.gotPackages[0] != "." {
		t.Errorf("fallback packages = %v, want [\".\"] (root-scoped, not whole suite)", runner.gotPackages)
	}
}

// TestAffectedSelectorErrorDegradesNotFails asserts a selector ERROR (e.g.
// coverage profile parse failure) does not crash the verifier: it degrades to the
// next strategy. Here coverage errors, import-graph declines, fallback wins.
func TestTestsAffectedSelectorErrorDegradesNotFails(t *testing.T) {
	root := t.TempDir()
	old, newBody := affectedFixture()
	writeModule(t, root, old)
	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "main.go", Patch: replaceFilePatch("main.go", old, newBody)}},
		Fixer: "test",
	}

	runner := &recordingRunner{}
	v := verify.NewTestsAffectedWith(runner.run,
		stubSelector{err: fmt.Errorf("coverage profile corrupt")},
		stubSelector{sel: testselect.Selection{Strategy: testselect.StrategyImportGraph}},
		testselect.NewPackageFallback(),
	)
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if !res.Passed {
		t.Fatalf("a single selector error must degrade, not fail the gate; got %+v", res)
	}
	if !strings.HasPrefix(res.Checks[0].Detail, "package-fallback") {
		t.Errorf("detail = %q, want degraded package-fallback", res.Checks[0].Detail)
	}
}

// TestAffectedFailsWhenSelectedTestsFail asserts the verifier FAILS (a skip) when
// the selected tests fail, carrying the strategy label + output in the detail.
func TestTestsAffectedFailsWhenSelectedTestsFail(t *testing.T) {
	root := t.TempDir()
	old, newBody := affectedFixture()
	writeModule(t, root, old)
	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "main.go", Patch: replaceFilePatch("main.go", old, newBody)}},
		Fixer: "test",
	}

	runner := &recordingRunner{fail: true}
	v := verify.NewTestsAffectedWith(runner.run,
		stubSelector{sel: testselect.Selection{
			Packages: []string{"./pkg/a"}, Tests: []string{"./pkg/a"},
			Strategy: testselect.StrategyImportGraph, OK: true,
		}},
	)
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if res.Passed {
		t.Fatal("failing selected tests must fail the gate (a skip)")
	}
	if !strings.Contains(res.Checks[0].Detail, "import-graph") {
		t.Errorf("failure detail should name the strategy used; got %q", res.Checks[0].Detail)
	}
}

// TestAffectedNeverMutatesRealTree: like compile, the verifier works on a scratch
// copy, so the real tree is byte-identical after Verify.
func TestTestsAffectedNeverMutatesRealTree(t *testing.T) {
	root := t.TempDir()
	old, newBody := affectedFixture()
	writeModule(t, root, old)
	before := hashTree(t, root)

	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "main.go", Patch: replaceFilePatch("main.go", old, newBody)}},
		Fixer: "test",
	}
	runner := &recordingRunner{}
	v := verify.NewTestsAffectedWith(runner.run,
		stubSelector{sel: testselect.Selection{
			Packages: []string{"."}, Strategy: testselect.StrategyPackageFallback, OK: true,
		}},
	)
	_ = v.Verify(context.Background(), diff, engine.Scope{Root: root})

	if after := hashTree(t, root); before != after {
		t.Fatalf("tests:affected MUTATED the real tree (hash changed)")
	}
}

// TestAffectedRefusesEmptyPackageRun guards the runner boundary: even if a buggy
// selector returned OK with no packages, the production goTestRunner refuses to
// run (which would imply the whole suite). Verified directly on the runner via a
// selection that yields no packages → the verifier reports no affected tests.
func TestTestsAffectedNoAffectedTestsIsHonestPass(t *testing.T) {
	root := t.TempDir()
	old, newBody := affectedFixture()
	writeModule(t, root, old)
	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "main.go", Patch: replaceFilePatch("main.go", old, newBody)}},
		Fixer: "test",
	}
	runner := &recordingRunner{}
	// All selectors decline (OK=false) — no affected tests for this change.
	v := verify.NewTestsAffectedWith(runner.run,
		stubSelector{sel: testselect.Selection{Strategy: testselect.StrategyCoverageMap}},
		stubSelector{sel: testselect.Selection{Strategy: testselect.StrategyImportGraph}},
		stubSelector{sel: testselect.Selection{Strategy: testselect.StrategyPackageFallback}},
	)
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if !res.Passed {
		t.Fatalf("no-affected-tests must be an honest pass, got %+v", res)
	}
	if len(runner.gotPackages) != 0 {
		t.Fatalf("runner should not have been invoked when no tests are affected; ran %v", runner.gotPackages)
	}
}
