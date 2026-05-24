package verify_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/verify"
)

// writeModule writes a tiny one-file Go module into dir so a real `go build`
// has something to compile. It is the hermetic fixture: a scratch module the
// test authored, NOT the ant repo building itself.
func writeModule(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module scratchmod\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(body), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
}

// replaceFilePatch builds a unified-diff patch that replaces a file's entire
// content (delete every old line, add every new line) — the worst-case patch a
// fixer could emit, exercising the applier end to end.
func replaceFilePatch(path, oldContent, newContent string) string {
	oldLines := splitForPatch(oldContent)
	newLines := splitForPatch(newContent)
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)
	fmt.Fprintf(&b, "@@ -1,%d +1,%d @@\n", len(oldLines), len(newLines))
	for _, ln := range oldLines {
		b.WriteString("-" + ln + "\n")
	}
	for _, ln := range newLines {
		b.WriteString("+" + ln + "\n")
	}
	return b.String()
}

func splitForPatch(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

const goodMain = "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"
const brokenMain = "package main\n\nfunc main() {\n\tthis is not valid go\n}\n"

// TestCompilePassesCleanDiff: a diff that keeps the module building passes,
// using the REAL go toolchain over a scratch module fixture (hermetic — it does
// not build the ant repo).
func TestCompilePassesCleanDiff(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; skipping real-build compile test")
	}
	root := t.TempDir()
	writeModule(t, root, goodMain)

	// A diff that rewrites main.go to a different-but-valid program.
	newBody := "package main\n\nfunc main() {\n\tprintln(\"hello again\")\n}\n"
	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "main.go", Patch: replaceFilePatch("main.go", goodMain, newBody)}},
		Fixer: "test",
	}

	v := verify.NewCompile(nil) // nil → real `go build ./...`
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if !res.Passed {
		t.Fatalf("clean diff should pass compile; got %+v", res)
	}
	if res.Checks[0].Name != verify.CheckCompile {
		t.Errorf("check name = %q, want %q", res.Checks[0].Name, verify.CheckCompile)
	}
}

// TestCompileFailsBuildBreakingDiff: a diff that introduces a syntax error fails
// WITH the build error in the detail.
func TestCompileFailsBuildBreakingDiff(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; skipping real-build compile test")
	}
	root := t.TempDir()
	writeModule(t, root, goodMain)

	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "main.go", Patch: replaceFilePatch("main.go", goodMain, brokenMain)}},
		Fixer: "test",
	}

	v := verify.NewCompile(nil)
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if res.Passed {
		t.Fatal("a build-breaking diff must fail compile")
	}
	if res.Checks[0].Passed {
		t.Error("compile check should be marked failed")
	}
	if res.Checks[0].Detail == "" {
		t.Error("a failed compile must carry the build error as detail (the skip reason)")
	}
}

// TestCompileNeverMutatesRealTree is the non-negotiable: the verifier applies the
// diff to a SCRATCH copy, so the real working tree is byte-identical before and
// after Verify — even for a build-breaking diff. Asserted by hashing the tree.
func TestCompileNeverMutatesRealTree(t *testing.T) {
	root := t.TempDir()
	writeModule(t, root, goodMain)
	before := hashTree(t, root)

	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "main.go", Patch: replaceFilePatch("main.go", goodMain, brokenMain)}},
		Fixer: "test",
	}

	// Inject a fake build so this test does not depend on the toolchain and stays
	// fast; the scratch-tree application is what we are asserting does not leak.
	fakeBuild := func(context.Context, string) ([]byte, error) {
		return []byte("build error: broken"), fmt.Errorf("exit status 1")
	}
	v := verify.NewCompile(fakeBuild)
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if res.Passed {
		t.Fatal("fake build reports failure; verifier should fail")
	}

	after := hashTree(t, root)
	if before != after {
		t.Fatalf("compile verifier MUTATED the real working tree (hash changed)\nbefore: %s\nafter:  %s", before, after)
	}
	// And the real file still holds the original content.
	got, _ := os.ReadFile(filepath.Join(root, "main.go"))
	if string(got) != goodMain {
		t.Errorf("real main.go was altered:\n%s", got)
	}
}

// TestCompileScratchPrepFailureIsSkipNotPanic: a malformed patch (context
// mismatch) becomes a failed check, never a panic.
func TestCompileScratchPrepFailureIsSkipNotPanic(t *testing.T) {
	root := t.TempDir()
	writeModule(t, root, goodMain)

	// A patch that claims to remove a line the file does not contain.
	bad := "--- a/main.go\n+++ b/main.go\n@@ -1,1 +1,0 @@\n-nonexistent line\n"
	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "main.go", Patch: bad}},
		Fixer: "test",
	}
	v := verify.NewCompile(func(context.Context, string) ([]byte, error) { return nil, nil })
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if res.Passed {
		t.Fatal("a malformed patch must fail (not pass, not panic)")
	}
	if !strings.Contains(res.Checks[0].Detail, "scratch") {
		t.Errorf("detail should explain the scratch-prep failure; got %q", res.Checks[0].Detail)
	}
}

// hashTree returns a stable hash of every regular file's relative path + content
// under root, so a test can prove the tree did not change.
func hashTree(t *testing.T, root string) string {
	t.Helper()
	var entries []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		entries = append(entries, rel+":"+string(b))
		return nil
	})
	if err != nil {
		t.Fatalf("hashTree walk: %v", err)
	}
	sort.Strings(entries)
	sum := sha256.Sum256([]byte(strings.Join(entries, "\x00")))
	return fmt.Sprintf("%x", sum[:])
}
