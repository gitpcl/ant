package verify_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/langmap"
	"github.com/gitpcl/ant/internal/engine/verify"
	"github.com/gitpcl/ant/internal/engine/verify/testselect"
)

// verifyExecMissingRunner returns a TestRunner that runs a binary guaranteed not
// to exist, so the real exec layer produces the exec-not-found error the
// verifier's missing-binary tolerance must convert to a clean skip.
func verifyExecMissingRunner(t *testing.T) verify.TestRunner {
	t.Helper()
	return func(ctx context.Context, dir string, _, _ []string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "ant-no-such-runner-xyz")
		cmd.Dir = dir
		return cmd.CombinedOutput()
	}
}

// writeFile writes a file (creating parent dirs) into the scratch tree so the
// co-located selectors find a real test file on disk.
func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// TestAffectedTSSelectsVitest: a .ts diff selects its co-located *.test.ts via the
// vitest selector and runs it through the injected runner — proving ts→vitest
// dispatch with a real selector against an on-disk test file.
func TestAffectedTSSelectsVitest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/util.ts", "export const a = 1;\n")
	writeFile(t, root, "src/util.test.ts", "import {a} from './util';\n")

	var ranPkgs []string
	runner := func(_ context.Context, _ string, packages, _ []string) ([]byte, error) {
		ranPkgs = append([]string(nil), packages...)
		return []byte("ok"), nil
	}
	v := verify.NewTestsAffectedForLang(langmap.TypeScript, runner, testselect.NewVitest())

	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "src/util.ts", Patch: addFilePatch("src/util.ts", "export const a = 2;\n")}},
		Fixer: "test",
	}
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if !res.Passed {
		t.Fatalf("ts diff with a co-located test should pass; got %+v", res)
	}
	if len(ranPkgs) != 1 || ranPkgs[0] != "src/util.test.ts" {
		t.Fatalf("vitest runner should get the co-located test file; got %v", ranPkgs)
	}
	if !strings.Contains(res.Checks[0].Detail, "vitest") {
		t.Errorf("detail should name the vitest strategy; got %q", res.Checks[0].Detail)
	}
}

// TestAffectedPySelectsPytest: a .py diff selects test_<name>.py and runs pytest.
func TestAffectedPySelectsPytest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkg/calc.py", "def add(a,b): return a+b\n")
	writeFile(t, root, "pkg/test_calc.py", "from calc import add\n")

	var ranPkgs []string
	runner := func(_ context.Context, _ string, packages, _ []string) ([]byte, error) {
		ranPkgs = append([]string(nil), packages...)
		return nil, nil
	}
	v := verify.NewTestsAffectedForLang(langmap.Python, runner, testselect.NewPytest())

	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "pkg/calc.py", Patch: addFilePatch("pkg/calc.py", "def add(a,b): return b+a\n")}},
		Fixer: "test",
	}
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if !res.Passed {
		t.Fatalf("py diff with a co-located test should pass; got %+v", res)
	}
	if len(ranPkgs) != 1 || ranPkgs[0] != "pkg/test_calc.py" {
		t.Fatalf("pytest runner should get the co-located test file; got %v", ranPkgs)
	}
}

// TestAffectedPHPSelectsPHPUnit: a .php diff selects <Name>Test.php and runs phpunit.
func TestAffectedPHPSelectsPHPUnit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/Money.php", "<?php class Money {}\n")
	writeFile(t, root, "src/MoneyTest.php", "<?php class MoneyTest {}\n")

	var ranPkgs []string
	runner := func(_ context.Context, _ string, packages, _ []string) ([]byte, error) {
		ranPkgs = append([]string(nil), packages...)
		return nil, nil
	}
	v := verify.NewTestsAffectedForLang(langmap.PHP, runner, testselect.NewPHPUnit())

	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "src/Money.php", Patch: addFilePatch("src/Money.php", "<?php class Money { public $x; }\n")}},
		Fixer: "test",
	}
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if !res.Passed {
		t.Fatalf("php diff with a co-located test should pass; got %+v", res)
	}
	if len(ranPkgs) != 1 || ranPkgs[0] != "src/MoneyTest.php" {
		t.Fatalf("phpunit runner should get the co-located test file; got %v", ranPkgs)
	}
}

// TestAffectedFailingTestFails: when the language runner reports a failure, the
// check FAILS with the test output as the reason.
func TestAffectedFailingTestFails(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/util.ts", "export const a = 1;\n")
	writeFile(t, root, "src/util.test.ts", "import {a} from './util';\n")

	runner := func(context.Context, string, []string, []string) ([]byte, error) {
		return []byte("FAIL src/util.test.ts"), os.ErrInvalid
	}
	v := verify.NewTestsAffectedForLang(langmap.TypeScript, runner, testselect.NewVitest())

	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "src/util.ts", Patch: addFilePatch("src/util.ts", "export const a = 2;\n")}},
		Fixer: "test",
	}
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if res.Passed {
		t.Fatal("a failing test run must fail tests:affected")
	}
	if !strings.Contains(res.Checks[0].Detail, "FAIL") {
		t.Errorf("failure detail should carry the runner output; got %q", res.Checks[0].Detail)
	}
}

// TestAffectedUnsupportedLanguageIsHonestSkip is THE REGRESSION GUARD for
// tests:affected: a diff in a language with NO registered runner is a visible
// skip-with-reason ("no test runner for <lang>"), never a vacuous pass. Here the
// verifier has only a TypeScript entry, and the diff is Ruby (unknown).
func TestAffectedUnsupportedLanguageIsHonestSkip(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "app.rb", "puts 'hi'\n")

	var ran bool
	runner := func(context.Context, string, []string, []string) ([]byte, error) {
		ran = true
		return nil, nil
	}
	v := verify.NewTestsAffectedForLang(langmap.TypeScript, runner, testselect.NewVitest())

	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "app.rb", Patch: addFilePatch("app.rb", "puts 'new'\n")}},
		Fixer: "test",
	}
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if ran {
		t.Error("no runner should fire for an unsupported-language diff")
	}
	if !strings.HasPrefix(res.Checks[0].Detail, "skipped:") {
		t.Errorf("unsupported-language tests:affected must be a SKIP-with-reason; detail=%q", res.Checks[0].Detail)
	}
	if !strings.Contains(res.Checks[0].Detail, "no test runner for") {
		t.Errorf("skip reason must name the missing runner; detail=%q", res.Checks[0].Detail)
	}
}

// TestAffectedUntrustedSpeciesDoesNotExecuteRepoCode is THE SECURITY REGRESSION
// GUARD for the Sprint-026 audit: a tests:affected runner executes repo-controlled
// code (conftest.py / vitest.config.ts / phpunit bootstrap / _test.go). An
// untrusted species (ScriptExecAllowed=false → ExecAllowed=false) must therefore
// NOT run any repo code — even though the language is supported and a co-located
// test file exists on disk. The verifier must skip with a trust reason and the
// injected runner must NEVER fire. This mirrors store.TestRepoSuppliedTrustFileIs
// Ignored / the scout assertScanSafe pattern: an untrusted surface stays inert.
func TestAffectedUntrustedSpeciesDoesNotExecuteRepoCode(t *testing.T) {
	root := t.TempDir()
	// A real, supported-language change WITH a co-located test on disk: absent the
	// gate, the verifier WOULD select pkg/test_calc.py and execute pytest over it.
	writeFile(t, root, "pkg/calc.py", "def add(a,b): return a+b\n")
	writeFile(t, root, "pkg/test_calc.py", "from calc import add\n")
	// A conftest.py is exactly the repo-controlled code pytest imports at
	// collection; its presence makes the "runs repo code" surface concrete.
	writeFile(t, root, "pkg/conftest.py", "raise SystemExit('untrusted repo code executed')\n")

	var ran bool
	runner := func(context.Context, string, []string, []string) ([]byte, error) {
		ran = true
		return nil, nil
	}
	// execAllowed=false: the untrusted/never-reviewed species posture.
	v := verify.NewTestsAffectedForLangGated(langmap.Python, false, runner, testselect.NewPytest())

	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "pkg/calc.py", Patch: addFilePatch("pkg/calc.py", "def add(a,b): return b+a\n")}},
		Fixer: "test",
	}
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})

	if ran {
		t.Fatal("untrusted tests:affected MUST NOT execute the repo test runner")
	}
	// A skip is a non-failing visible outcome in this codebase (like the
	// unsupported-language and missing-binary skips). The security invariant is
	// that NO repo code ran and the reason is the trust gate — asserted below — not
	// a hard fail. What it must NOT be is a clean GREEN that hides execution; the
	// "skipped:" + "not yet trusted" detail is exactly that honest signal.
	if !strings.HasPrefix(res.Checks[0].Detail, "skipped:") {
		t.Errorf("untrusted tests:affected must surface as a skip-with-reason; detail=%q", res.Checks[0].Detail)
	}
	if !strings.Contains(res.Checks[0].Detail, "not yet trusted") {
		t.Errorf("skip reason must explain the trust gate; detail=%q", res.Checks[0].Detail)
	}

	// And the inverse: the SAME diff under a trusted species DOES run the runner —
	// proving the gate is the only thing holding execution back, not a broken setup.
	var trustedRan bool
	trustedRunner := func(context.Context, string, []string, []string) ([]byte, error) {
		trustedRan = true
		return nil, nil
	}
	tv := verify.NewTestsAffectedForLangGated(langmap.Python, true, trustedRunner, testselect.NewPytest())
	if tr := tv.Verify(context.Background(), diff, engine.Scope{Root: root}); !tr.Passed {
		t.Fatalf("trusted tests:affected should run and pass; got %+v", tr)
	}
	if !trustedRan {
		t.Fatal("trusted tests:affected should have executed the runner")
	}
}

// TestAffectedMissingRunnerBinaryIsCleanSkip: a supported language whose runner
// binary is absent is a clean skip (CI without vitest/pytest/phpunit stays green).
func TestAffectedMissingRunnerBinaryIsCleanSkip(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/util.ts", "export const a = 1;\n")
	writeFile(t, root, "src/util.test.ts", "import {a} from './util';\n")

	v := verify.NewTestsAffectedForLang(langmap.TypeScript, verifyExecMissingRunner(t), testselect.NewVitest())

	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "src/util.ts", Patch: addFilePatch("src/util.ts", "export const a = 2;\n")}},
		Fixer: "test",
	}
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if !res.Passed {
		t.Fatalf("a missing runner binary must be a clean skip; got %+v", res)
	}
	if !strings.HasPrefix(res.Checks[0].Detail, "skipped:") {
		t.Errorf("missing runner binary must surface as a skip; detail=%q", res.Checks[0].Detail)
	}
}
