package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	store "github.com/gitpcl/ant/internal/engine/store"
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
	for _, cmd := range []string{"scout", "fix", "review", "apply", "init", "doctor", "explain", "species"} {
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

func TestDoctorJSONEmitsReportAndCIExitCode(t *testing.T) {
	// `ant doctor --json` against a temp dir with no ant.toml emits the
	// single-document report (top-level "ready" + "checks"). The exit code is
	// CI-correct: 0 when the required capabilities are present, 2 (operational)
	// when a required tool is missing. We assert the contract fields are present
	// and that the exit code agrees with the rendered "ready" value — the CLI is
	// a thin renderer over the engine's Report (TECHSPEC §3), so we do not pin
	// which tools happen to be installed on the test host.
	dir := t.TempDir()
	out, code := runCmd(t, "doctor", dir, "--json")

	for _, field := range []string{`"ready"`, `"checks"`, `"name"`, `"status"`, `"required"`, `"detail"`} {
		if !strings.Contains(out, field) {
			t.Errorf("doctor --json missing field %s:\n%s", field, out)
		}
	}
	ready := strings.Contains(out, `"ready": true`)
	if ready && code != engine.ExitOK {
		t.Errorf("doctor reported ready but exit = %d, want 0", code)
	}
	if !ready && code != engine.ExitOperational {
		t.Errorf("doctor reported not-ready but exit = %d, want %d", code, engine.ExitOperational)
	}
}

// TestExplainResolvesRunAndFinding drives `ant explain` through the front door
// against a real seeded local store: a run reference and a `<runID>#<index>`
// finding reference both render with --json, and a missing run is the CI-correct
// operational exit (2). The CLI is a thin renderer over the engine's Detail
// (TECHSPEC §3), so the assertions pin the contract shape, not the engine logic
// (covered by internal/engine/explain).
func TestExplainResolvesRunAndFinding(t *testing.T) {
	dir := t.TempDir()
	run := engine.Run{
		ID:        "fix-explain-test",
		StartedAt: "2026-05-31T10:00:00Z",
		Scope:     engine.Scope{Root: "."},
		Findings: []engine.Finding{{
			Species:  "unused-import",
			File:     "main.go",
			Span:     engine.Span{StartLine: 3, StartCol: 1, EndLine: 3, EndCol: 12},
			Severity: engine.SeverityLow,
			Message:  "unused import",
		}},
	}
	if err := store.New(dir).SaveRun(run); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	// Run reference, --json.
	out, code := runCmd(t, "explain", run.ID, "--path", dir, "--json")
	if code != engine.ExitOK {
		t.Fatalf("explain run exit = %d, want 0:\n%s", code, out)
	}
	for _, field := range []string{`"kind": "run"`, `"runId"`, `"findings"`} {
		if !strings.Contains(out, field) {
			t.Errorf("explain run --json missing %s:\n%s", field, out)
		}
	}

	// Finding reference, --json.
	out, code = runCmd(t, "explain", run.ID+"#0", "--path", dir, "--json")
	if code != engine.ExitOK {
		t.Fatalf("explain finding exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, `"kind": "finding"`) || !strings.Contains(out, "unused-import") {
		t.Errorf("explain finding --json missing finding detail:\n%s", out)
	}

	// Missing run is operational (exit 2).
	_, code = runCmd(t, "explain", "no-such-run", "--path", dir)
	if code != engine.ExitOperational {
		t.Errorf("explain missing run exit = %d, want %d", code, engine.ExitOperational)
	}
}

// TestSpeciesValidateThroughFrontDoor drives `ant species validate` end to end:
// a well-formed local folder validates with exit 0 and a JSON report, and a
// malformed one exits 2 (operational, CI-correct) with the problem rendered. The
// CLI is a thin renderer over species.Validate (TECHSPEC §3), so the assertions
// pin the contract shape + exit code, not the rule logic (covered by
// internal/engine/species).
func TestSpeciesValidateThroughFrontDoor(t *testing.T) {
	// Valid folder: an ast-grep deterministic species with its rule file present.
	valid := t.TempDir()
	writeFile(t, filepath.Join(valid, "species.toml"), `name = "frontdoor-valid"
severity = "low"
[detector]
kind = "ast-grep"
rule = "detect.yml"
[fix]
kind = "deterministic"
transform = "delete-match"
[verify]
checks = ["compile"]
`)
	writeFile(t, filepath.Join(valid, "detect.yml"), "id: frontdoor-valid\n")

	out, code := runCmd(t, "species", "validate", valid, "--json")
	if code != engine.ExitOK {
		t.Fatalf("validate valid folder exit = %d, want 0:\n%s", code, out)
	}
	for _, field := range []string{`"ok": true`, `"path"`, `"manifest"`, `"capabilities"`} {
		if !strings.Contains(out, field) {
			t.Errorf("species validate --json missing %s:\n%s", field, out)
		}
	}

	// Invalid folder: the manifest references a detect rule that does not exist,
	// so validation fails and the command exits operational (2).
	invalid := t.TempDir()
	writeFile(t, filepath.Join(invalid, "species.toml"), `name = "frontdoor-invalid"
severity = "low"
[detector]
kind = "ast-grep"
rule = "missing.yml"
[fix]
kind = "deterministic"
transform = "delete-match"
[verify]
checks = ["compile"]
`)

	out, code = runCmd(t, "species", "validate", invalid)
	if code != engine.ExitOperational {
		t.Errorf("validate invalid folder exit = %d, want %d:\n%s", code, engine.ExitOperational, out)
	}
	if !strings.Contains(out, "invalid:") || !strings.Contains(out, "file not found") {
		t.Errorf("species validate did not report the missing rule file:\n%s", out)
	}
}

// writeFile writes content to path, creating parent dirs, failing the test on
// error. A tiny local helper for the species-validate front-door fixtures.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
