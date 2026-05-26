package fix_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/fix"
)

// TestTool_RealFakeBinaryOnPATH drives the REAL execRunner (no injected stub)
// against a fake formatter script on PATH, proving the production read→exec→diff
// path end-to-end without any installed formatter (sprint contract: CI must not
// depend on gofmt/prettier/ruff/eslint).
func TestTool_RealFakeBinaryOnPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-binary-on-PATH test uses a POSIX shell script; skipped on Windows")
	}
	// Fake formatter: strip trailing whitespace in place (a gofmt stand-in).
	bin := t.TempDir()
	script := "#!/bin/sh\nf=\"$2\"\nsed 's/[[:space:]]*$//' \"$f\" > \"$f.tmp\" && mv \"$f.tmp\" \"$f\"\n"
	if err := os.WriteFile(filepath.Join(bin, "fakefmt"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake formatter: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Drifted file (trailing spaces) in a temp working tree; chdir so the
	// tool-runner reads it by root-relative path, as the colony does.
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "drift.txt"), []byte("clean line\ndrift here   \n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	t.Chdir(work)

	fixer, err := fix.NewTool(fix.ToolConfig{Command: "fakefmt", Args: []string{"-w", fix.PlaceholderFile}})
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	diff, err := fixer.Fix(context.Background(), engine.FixTask{Context: engine.CodeContext{File: "drift.txt"}})
	if err != nil {
		t.Fatalf("Fix via real fake binary: %v", err)
	}
	if len(diff.Files) != 1 || diff.Files[0].Path != "drift.txt" {
		t.Fatalf("unexpected diff files: %+v", diff.Files)
	}
	if !strings.HasPrefix(diff.Fixer, "tool (fakefmt") {
		t.Errorf("provenance = %q, want tool (fakefmt...)", diff.Fixer)
	}
	// The captured patch must turn the drifted line into the trimmed line.
	if !strings.Contains(diff.Files[0].Patch, "-drift here") || !strings.Contains(diff.Files[0].Patch, "+drift here") {
		t.Errorf("patch did not capture the trailing-whitespace fix:\n%s", diff.Files[0].Patch)
	}
}
