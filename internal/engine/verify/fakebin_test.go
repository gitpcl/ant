package verify_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/verify"
)

// writeFakeFormatter writes an executable shell script that, run as
// `<name> -w <file>`, rewrites <file> via the given sed-style body, and prepends
// its dir to PATH for the test. It is the deterministic FAKE tool binary the
// sprint contract requires: CI must NOT depend on real gofmt/prettier/ruff/eslint
// being installed, so the prereqs are validated against a script ON PATH that the
// REAL execToolRunner resolves and execs (no injected stub).
func writeFakeFormatter(t *testing.T, name, scriptBody string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-binary-on-PATH test uses a POSIX shell script; skipped on Windows")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" + scriptBody + "\n"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake formatter: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestIdempotence_RealFakeBinaryOnPATH drives the REAL execToolRunner (no
// injected stub) against a fake formatter script on PATH. A STABLE script (writes
// the file but produces no change on a normalized input) passes; an OSCILLATING
// script (appends a marker every run) fails — proving the production exec path,
// not just the mock.
func TestIdempotence_RealFakeBinaryOnPATH(t *testing.T) {
	t.Run("stable converges", func(t *testing.T) {
		// A stable formatter: strip trailing whitespace. On already-trimmed input a
		// second run changes nothing.
		writeFakeFormatter(t, "fakefmt", `f="$2"; sed 's/[[:space:]]*$//' "$f" > "$f.tmp" && mv "$f.tmp" "$f"`)
		scope, diff := realFixture(t, "f.txt", "already clean\nno trailing space\n")
		v := verify.NewFormatterIdempotence(verify.ToolSpec{Command: "fakefmt", Args: []string{"-w", verify.PlaceholderFile}}, nil)
		if res := v.Verify(context.Background(), diff, scope); !res.Passed {
			t.Fatalf("stable fake formatter on PATH failed idempotence: %v", res.Checks)
		}
	})

	t.Run("oscillating never converges", func(t *testing.T) {
		// Every run appends a marker line — the file never stabilizes.
		writeFakeFormatter(t, "oscfmt", `f="$2"; echo "// touched" >> "$f"`)
		scope, diff := realFixture(t, "f.txt", "line one\n")
		v := verify.NewFormatterIdempotence(verify.ToolSpec{Command: "oscfmt", Args: []string{"-w", verify.PlaceholderFile}}, nil)
		if res := v.Verify(context.Background(), diff, scope); res.Passed {
			t.Fatal("oscillating fake formatter on PATH PASSED idempotence, want fail")
		}
	})
}

// realFixture seeds a working-tree file and returns the scope + an identity diff
// reproducing it, mirroring idempotenceFixture but in the external test package
// so it exercises only the exported API.
func realFixture(t *testing.T, name, content string) (engine.Scope, engine.ProposedDiff) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	var b strings.Builder
	b.WriteString("--- a/" + name + "\n")
	b.WriteString("+++ b/" + name + "\n")
	b.WriteString("@@ -1," + itoaExt(len(lines)) + " +1," + itoaExt(len(lines)) + " @@\n")
	for _, ln := range lines {
		b.WriteString(" " + ln + "\n")
	}
	return engine.Scope{Root: dir}, engine.ProposedDiff{Files: []engine.FileDiff{{Path: name, Patch: b.String()}}}
}

func itoaExt(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
