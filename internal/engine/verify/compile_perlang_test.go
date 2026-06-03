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
)

// verifyExecMissing returns a BuildCommand that runs a binary guaranteed not to
// exist, so the real exec layer produces the exec-not-found error the verifier's
// missing-binary tolerance must treat as a clean skip — proving the tolerance
// against the real error type, not a faked one.
func verifyExecMissing(t *testing.T) verify.BuildCommand {
	t.Helper()
	return func(ctx context.Context, dir string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "ant-no-such-binary-xyz", "--lint")
		cmd.Dir = dir
		return cmd.CombinedOutput()
	}
}

// writePHPModule writes a tiny scratch tree with one PHP file so a per-file PHP
// builder has a real file to resolve and lint (hermetic — no live php needed;
// the builder itself is a fake in these tests).
func writePHPModule(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte(body), 0o644); err != nil {
		t.Fatalf("write index.php: %v", err)
	}
}

const phpBody = "<?php\necho \"hello\";\n"

// addFilePatch builds a unified-diff that ADDS a brand-new file (so the scratch
// tree apply creates it). Used to drive a .php / .py diff through compile.
func addFilePatch(path, content string) string {
	lines := splitForPatch(content)
	var b strings.Builder
	b.WriteString("--- /dev/null\n+++ b/" + path + "\n")
	b.WriteString("@@ -0,0 +1," + itoaTest(len(lines)) + " @@\n")
	for _, ln := range lines {
		b.WriteString("+" + ln + "\n")
	}
	return b.String()
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestCompilePHPDiffRunsPHPBuilder is the SPIKE proof: a .php diff resolves to
// the php language and runs the php builder (here a fake), NOT the Go builder.
func TestCompilePHPDiffRunsPHPBuilder(t *testing.T) {
	root := t.TempDir()
	writePHPModule(t, root, phpBody)

	var phpRan bool
	table := verify.BuildTable{
		langmap.Go: func(context.Context, string) ([]byte, error) {
			t.Error("go builder must NOT run for a .php diff")
			return nil, nil
		},
		langmap.PHP: func(context.Context, string) ([]byte, error) {
			phpRan = true
			return nil, nil
		},
	}

	// A diff that touches a .php file.
	newBody := "<?php\necho \"hello again\";\n"
	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "index.php", Patch: replaceFilePatch("index.php", phpBody, newBody)}},
		Fixer: "test",
	}

	v := verify.NewCompile(table)
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if !res.Passed {
		t.Fatalf("clean php diff should pass; got %+v", res)
	}
	if !phpRan {
		t.Fatal("the php builder was not invoked for a .php diff (language dispatch failed)")
	}
}

// TestCompilePHPBuildFailureFails: when the php builder reports a lint error, the
// compile check FAILS with the error as the detail (the skip reason).
func TestCompilePHPBuildFailureFails(t *testing.T) {
	root := t.TempDir()
	writePHPModule(t, root, phpBody)

	table := verify.BuildTable{
		langmap.PHP: func(context.Context, string) ([]byte, error) {
			return []byte("PHP Parse error: syntax error"), os.ErrInvalid
		},
	}
	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "index.php", Patch: replaceFilePatch("index.php", phpBody, "<?php bad")}},
		Fixer: "test",
	}
	v := verify.NewCompile(table)
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if res.Passed {
		t.Fatal("a php lint error must fail compile")
	}
	if !strings.Contains(res.Checks[0].Detail, "Parse error") {
		t.Errorf("failed php compile must carry the lint output as detail; got %q", res.Checks[0].Detail)
	}
}

// TestCompileUnknownLanguageIsHonestSkipNotPass is THE REGRESSION GUARD for the
// vacuous-pass hole (Sprint 026 core goal): a diff in a language with NO
// registered builder must be a VISIBLE skip-with-reason — passing the gate (so a
// fix is not blocked by a missing checker) but with a "no compile checker for
// <lang>" reason in the detail, NEVER a silent green that hides an unchecked
// diff. The Go-only table here has no checker for a .rb (unknown) file.
func TestCompileUnknownLanguageIsHonestSkipNotPass(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.rb"), []byte("puts 'hi'\n"), 0o644); err != nil {
		t.Fatalf("write app.rb: %v", err)
	}

	var goRan bool
	table := verify.BuildTable{
		langmap.Go: func(context.Context, string) ([]byte, error) {
			goRan = true
			return nil, nil
		},
	}
	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "app.rb", Patch: addFilePatch("new.rb", "puts 'new'\n")}},
		Fixer: "test",
	}
	v := verify.NewCompile(table)
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})

	// It must NOT run the Go builder for a Ruby diff.
	if goRan {
		t.Error("the go builder ran for an unknown-language (.rb) diff — wrong dispatch")
	}
	// The detail MUST be an honest skip-with-reason, never a plain pass.
	detail := res.Checks[0].Detail
	if !strings.HasPrefix(detail, "skipped:") {
		t.Errorf("unsupported-language compile must be a SKIP-with-reason, not a plain pass; detail=%q", detail)
	}
	if !strings.Contains(detail, "no compile checker for") {
		t.Errorf("skip reason must name the missing checker; detail=%q", detail)
	}
	if !strings.Contains(detail, langmap.Unknown) {
		t.Errorf("skip reason must name the unresolved language; detail=%q", detail)
	}
}

// TestCompileMissingBinaryIsCleanSkip: when the language IS supported but its
// toolchain binary is absent, the verifier produces a clean skip (CI without the
// toolchain stays green), not a failure.
func TestCompileMissingBinaryIsCleanSkip(t *testing.T) {
	root := t.TempDir()
	writePHPModule(t, root, phpBody)

	table := verify.BuildTable{
		// Simulate `php` not on PATH by running a definitely-absent binary.
		langmap.PHP: verifyExecMissing(t),
	}
	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "index.php", Patch: replaceFilePatch("index.php", phpBody, phpBody)}},
		Fixer: "test",
	}
	v := verify.NewCompile(table)
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if !res.Passed {
		t.Fatalf("a missing toolchain binary must be a clean skip (pass), got %+v", res)
	}
	if !strings.HasPrefix(res.Checks[0].Detail, "skipped:") {
		t.Errorf("missing-binary must surface as a skip; detail=%q", res.Checks[0].Detail)
	}
}
