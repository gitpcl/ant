package verify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
)

// idempotenceFixture writes a post-fix file into a temp scope and returns the
// scope plus a whole-file diff that produces that content from an empty original.
// The idempotence verifier applies the diff to a scratch copy and re-runs the
// tool there; a fixture file lets the fake runner read/write a real path without
// any live formatter (sprint contract, TECHSPEC §12).
func idempotenceFixture(t *testing.T, name, postFix string) (engine.Scope, engine.ProposedDiff) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	// Seed the working tree with the SAME content the diff produces, so the
	// scratch copy + apply yields exactly postFix (the diff is a no-op identity
	// patch here; idempotence only cares about the SECOND tool run's effect).
	if err := os.WriteFile(path, []byte(postFix), 0o600); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}
	scope := engine.Scope{Root: dir}
	// An identity diff: one context line per source line (no `-`/`+`), so apply
	// reproduces postFix verbatim into the scratch tree.
	var b strings.Builder
	b.WriteString("--- a/" + name + "\n")
	b.WriteString("+++ b/" + name + "\n")
	lines := strings.Split(strings.TrimSuffix(postFix, "\n"), "\n")
	b.WriteString("@@ -1," + itoa(len(lines)) + " +1," + itoa(len(lines)) + " @@\n")
	for _, ln := range lines {
		b.WriteString(" " + ln + "\n")
	}
	diff := engine.ProposedDiff{Files: []engine.FileDiff{{Path: name, Patch: b.String()}}}
	return scope, diff
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// TestIdempotence_StableFormatterPasses is the core feature-2 assertion: a stable
// formatter (a second run makes NO further changes) passes the idempotence check.
func TestIdempotence_StableFormatterPasses(t *testing.T) {
	scope, diff := idempotenceFixture(t, "f.go", "package p\nvar X = 1\n")
	stable := func(_ context.Context, _ string, args []string, _ string) error {
		// A stable formatter leaves the (already-formatted) file untouched.
		_ = args
		return nil
	}
	v := NewFormatterIdempotence(ToolSpec{Command: "stablefmt", Args: []string{"-w", PlaceholderFile}}, stable)
	res := v.Verify(context.Background(), diff, scope)
	if !res.Passed {
		t.Fatalf("stable formatter failed idempotence: %v", res.Checks)
	}
	if len(res.Checks) != 1 || res.Checks[0].Name != CheckFormatterIdempotence {
		t.Errorf("checks = %v, want one %q check", res.Checks, CheckFormatterIdempotence)
	}
}

// TestIdempotence_OscillatingFormatterFails is the trust-signal assertion: a
// non-idempotent (oscillating) formatter — one whose second run STILL changes the
// file — fails the check WITH detail, rather than the colony looping forever.
func TestIdempotence_OscillatingFormatterFails(t *testing.T) {
	scope, diff := idempotenceFixture(t, "f.go", "package p\nvar X = 1\n")
	oscillating := func(_ context.Context, _ string, args []string, _ string) error {
		// Model a tool that never converges: every run flips the content.
		file := args[len(args)-1]
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		flipped := string(b) + "// touched\n"
		return os.WriteFile(file, []byte(flipped), 0o600)
	}
	v := NewFormatterIdempotence(ToolSpec{Command: "oscfmt", Args: []string{"-w", PlaceholderFile}}, oscillating)
	res := v.Verify(context.Background(), diff, scope)
	if res.Passed {
		t.Fatal("oscillating formatter PASSED idempotence, want a fail (a tool that never converges must not be trusted)")
	}
	detail := res.Checks[0].Detail
	if !strings.Contains(detail, "not idempotent") && !strings.Contains(detail, "NOT idempotent") {
		t.Errorf("fail detail = %q, want it to name non-idempotence", detail)
	}
	if !strings.Contains(detail, "oscfmt") {
		t.Errorf("fail detail = %q, want it to name the tool", detail)
	}
}

// TestIdempotence_MissingToolFailsCleanly asserts a tool that cannot be run is a
// FAILED check (a surfaced skip), never a panic. Uses the real exec runner
// against a name that does not resolve.
func TestIdempotence_MissingToolFailsCleanly(t *testing.T) {
	scope, diff := idempotenceFixture(t, "f.go", "package p\n")
	v := NewFormatterIdempotence(ToolSpec{Command: "ant-no-such-tool-xyz", Args: []string{PlaceholderFile}}, nil)
	res := v.Verify(context.Background(), diff, scope)
	if res.Passed {
		t.Fatal("missing tool PASSED idempotence, want a clean fail")
	}
}

// TestIdempotence_NoCommandFails asserts a misconfigured species (no tool
// command) fails the check with a clear reason rather than silently passing.
func TestIdempotence_NoCommandFails(t *testing.T) {
	scope, diff := idempotenceFixture(t, "f.go", "package p\n")
	v := NewFormatterIdempotence(ToolSpec{}, func(context.Context, string, []string, string) error { return nil })
	res := v.Verify(context.Background(), diff, scope)
	if res.Passed {
		t.Fatal("idempotence with no declared command PASSED, want a clear fail")
	}
}
