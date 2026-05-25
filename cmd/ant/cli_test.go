package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
)

// runCmd builds the command tree, captures stdout+stderr, runs it against args,
// and returns the combined output and the exit code the CLI would return.
func runCmd(t *testing.T, args ...string) (string, int) {
	t.Helper()
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	code := executeWithExitCode(root)
	return out.String(), code
}

func TestHelpListsEveryCommand(t *testing.T) {
	out, code := runCmd(t, "--help")
	if code != engine.ExitOK {
		t.Errorf("--help exit = %d, want 0", code)
	}
	// Every command from TECHSPEC §7 must appear in help.
	for _, cmd := range []string{"scout", "fix", "review", "apply", "init", "species"} {
		if !strings.Contains(out, cmd) {
			t.Errorf("--help missing command %q:\n%s", cmd, out)
		}
	}
	// Global flags on the root.
	for _, flag := range []string{"--json", "--fail-on", "--config", "--fixer", "--model", "--concurrency"} {
		if !strings.Contains(out, flag) {
			t.Errorf("--help missing global flag %q:\n%s", flag, out)
		}
	}
}

func TestSpeciesHelpListsChildren(t *testing.T) {
	out, code := runCmd(t, "species", "--help")
	if code != engine.ExitOK {
		t.Errorf("species --help exit = %d, want 0", code)
	}
	for _, child := range []string{"list", "install", "remove"} {
		if !strings.Contains(out, child) {
			t.Errorf("species --help missing child %q:\n%s", child, out)
		}
	}
}

// TestReviewEmptyStateExitsCleanly asserts `ant review` with no staged diffs
// prints the empty-state screen and exits 0 (review-interaction.md §5.1). It is
// a clean front-door path that needs no fix run.
func TestReviewEmptyStateExitsCleanly(t *testing.T) {
	out, code := runCmd(t, "review", "--path", t.TempDir())
	if code != engine.ExitOK {
		t.Errorf("review on an empty store exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, "No staged diffs were found") {
		t.Errorf("review empty state should print the no-diffs screen:\n%s", out)
	}
}

// TestApplyNothingStagedExitsCleanly asserts `ant apply` with nothing staged is
// success (nothing to do is not an error).
func TestApplyNothingStagedExitsCleanly(t *testing.T) {
	out, code := runCmd(t, "apply", "--path", t.TempDir())
	if code != engine.ExitOK {
		t.Errorf("apply with nothing staged exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, "Nothing to apply") {
		t.Errorf("apply with nothing staged should say so:\n%s", out)
	}
}

// TestSpeciesStubsStillStubbed keeps the species leaves' clean-stub guarantee
// (they are out of scope this sprint). fix/review/apply are now implemented and
// covered by their own command + engine tests.
func TestFixReviewApplyHelpExists(t *testing.T) {
	for _, cmd := range []string{"fix", "review", "apply"} {
		out, code := runCmd(t, cmd, "--help")
		if code != engine.ExitOK {
			t.Errorf("%s --help exit = %d, want 0", cmd, code)
		}
		if strings.Contains(out, "not yet implemented") {
			t.Errorf("%s should be implemented, not a stub:\n%s", cmd, out)
		}
	}
}

// TestSpeciesListShowsBuiltins confirms `species list` is wired to the resolver
// + trust authority (not a stub): against an empty working tree it succeeds and
// lists the embedded built-ins with their effective trust. The engine package's
// resolve/trust tests cover the decision logic; this asserts the CLI renders it.
func TestSpeciesListShowsBuiltins(t *testing.T) {
	dir := t.TempDir()
	out, code := runCmd(t, "species", "list", "--path", dir)
	if code != engine.ExitOK {
		t.Fatalf("species list exit = %d, want 0:\n%s", code, out)
	}
	if strings.Contains(out, "not yet implemented") {
		t.Errorf("species list should be implemented, not a stub:\n%s", out)
	}
	// The embedded set (ADR-0002) must appear, with the table header and the
	// built-in origin column.
	for _, want := range []string{"NAME", "ORIGIN", "TRUST", "unused-import", "built-in"} {
		if !strings.Contains(out, want) {
			t.Errorf("species list missing %q:\n%s", want, out)
		}
	}
}

// TestSpeciesRemoveProtectsBuiltin confirms `species remove` refuses an embedded
// built-in with a non-zero (operational) exit — built-ins ship in the binary and
// are not removable. The engine package's remove_test.go covers the disk delete +
// trust clear for installed species.
func TestSpeciesRemoveProtectsBuiltin(t *testing.T) {
	dir := t.TempDir()
	out, code := runCmd(t, "species", "remove", "unused-import", "--path", dir)
	if code != engine.ExitOperational {
		t.Errorf("species remove built-in exit = %d, want %d (operational/protected):\n%s",
			code, engine.ExitOperational, out)
	}
	out, code = runCmd(t, "species", "remove", "ghost-species", "--path", dir)
	if code != engine.ExitOperational {
		t.Errorf("species remove missing exit = %d, want %d (operational):\n%s",
			code, engine.ExitOperational, out)
	}
}

// TestSpeciesInstallIsImplemented confirms `species install` is wired to the
// engine (not a stub): its --help renders without the stub line and requires the
// <git-url> argument. It is the security-stage feature this sprint. We do not
// drive a real clone here (no network); the engine package's install_test.go
// covers the clone+validate+no-exec behavior with local fixture repos.
func TestSpeciesInstallIsImplemented(t *testing.T) {
	out, code := runCmd(t, "species", "install", "--help")
	if code != engine.ExitOK {
		t.Errorf("species install --help exit = %d, want 0", code)
	}
	if strings.Contains(out, "not yet implemented") {
		t.Errorf("species install should be implemented, not a stub:\n%s", out)
	}
	// Missing the required <git-url> argument is a usage error (non-zero), not a
	// silent no-op.
	if _, code := runCmd(t, "species", "install"); code == engine.ExitOK {
		t.Errorf("species install with no URL should fail (requires <git-url>)")
	}
}

// freshSpeciesManifest is a well-formed installed species that REQUESTS
// auto-apply. It exists to prove the §6.3 freshly-installed override: a brand-new
// installed species must still list as propose-only despite auto_apply=true,
// until its output is reviewed once.
const freshSpeciesManifest = `name = "fresh"
description = "freshly installed, requests auto-apply"
severity = "medium"
languages = ["go"]
auto_apply = true

[detector]
kind = "ast-grep"
rule = "detect.yml"

[fix]
kind = "deterministic"
transform = "delete-match"

[verify]
checks = ["compile"]
`

// writeFreshInstalledSpecies places the fresh fixture species under
// <root>/.ant/species/fresh so `species list --path <root>` discovers it as a
// user-installed species with no tracked trust state (the brand-new default).
func writeFreshInstalledSpecies(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".ant", "species", "fresh")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "species.toml"), []byte(freshSpeciesManifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "detect.yml"), []byte("id: fresh\n"), 0o644); err != nil {
		t.Fatalf("write rule: %v", err)
	}
}

// TestSpeciesListMarksFreshlyInstalled is the load-bearing list assertion: an
// installed species whose manifest says auto_apply=true is still shown as
// propose-only, distinctly marked as freshly installed (gated until reviewed) —
// the §6.3 override surfacing in the CLI exactly as the apply path would gate it.
func TestSpeciesListMarksFreshlyInstalled(t *testing.T) {
	root := t.TempDir()
	writeFreshInstalledSpecies(t, root)

	out, code := runCmd(t, "species", "list", "--path", root)
	if code != engine.ExitOK {
		t.Fatalf("species list exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, "fresh") {
		t.Fatalf("species list missing the installed species:\n%s", out)
	}
	// The fresh species line must show the distinct freshly-installed marker, NOT
	// bare auto-apply (its manifest auto_apply=true is overridden until reviewed).
	var freshLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "fresh") {
			freshLine = line
			break
		}
	}
	if freshLine == "" {
		t.Fatalf("could not find the 'fresh' row in:\n%s", out)
	}
	if !strings.Contains(freshLine, "installed") {
		t.Errorf("fresh species should show 'installed' origin: %q", freshLine)
	}
	if !strings.Contains(freshLine, "new") || !strings.Contains(freshLine, "propose-only") {
		t.Errorf("freshly-installed species must be marked propose-only (new): %q", freshLine)
	}
	if strings.Contains(freshLine, "\tauto-apply") || strings.HasSuffix(strings.TrimSpace(freshLine), "auto-apply") {
		t.Errorf("freshly-installed species must NOT show bare auto-apply: %q", freshLine)
	}
}

func TestBadSeverityFlagIsOperationalExit(t *testing.T) {
	// --fail-on with a bad value must fail fast at exit 2 (operational, bad
	// input) before any scout run — never trust input past the boundary.
	_, code := runCmd(t, "scout", ".", "--fail-on=bogus")
	if code != engine.ExitOperational {
		t.Errorf("bad --fail-on exit = %d, want %d (operational)", code, engine.ExitOperational)
	}
	_, code = runCmd(t, "scout", ".", "--severity=nope")
	if code != engine.ExitOperational {
		t.Errorf("bad --severity exit = %d, want %d (operational)", code, engine.ExitOperational)
	}
}

func TestBareAntAndScoutShareTheSamePath(t *testing.T) {
	// Bare `ant` is an alias for scout (ADR 0001). Both reach the same handler;
	// with ast-grep absent locally both classify as operational (exit 2) rather
	// than crashing — proving the alias is wired and the missing-binary path is
	// graceful. (When ast-grep is present, both exit 0 on a clean tree.)
	_, bareCode := runCmd(t, "/nonexistent-scope-path-xyz")
	_, scoutCode := runCmd(t, "scout", "/nonexistent-scope-path-xyz")
	if bareCode != scoutCode {
		t.Errorf("bare ant (%d) and scout (%d) diverged; they must share the scout path", bareCode, scoutCode)
	}
}
