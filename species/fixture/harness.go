// Package fixture is the reusable species fixture-test harness (TECHSPEC §12,
// Sprint 008). Given a built-in species folder and a testdata repo seeded with
// the smell that species targets, it runs the REAL production pipeline —
// detect → fix → verify — and asserts the produced patch against a committed
// golden diff.
//
// It is deliberately species-agnostic so it carries the M2 deterministic species
// (unused-import, dead-code) AND the M3/M4 LLM species (n+1-query, missing-await,
// nil-deref, ai-slop) without change: the species' detector and fix strategy come
// from its species.toml through the same registry the CLI uses, and the Fixer is
// supplied by a factory so an LLM species can inject a recorded/stub fixer
// (TECHSPEC §10 — no live model in CI) exactly as a deterministic species lets the
// harness build the real delete-match fixer.
//
// Detection is a plugin boundary (TECHSPEC §2): the harness shells out to ast-grep
// through the production adapter. When ast-grep is not installed, RunCase skips
// (it never fails) so the suite stays green in environments without the binary,
// while proving genuine end-to-end behavior wherever ast-grep is present.
package fixture

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/fix"
	"github.com/gitpcl/ant/internal/engine/species"
	"github.com/gitpcl/ant/internal/engine/verify"
)

// astGrepBinary is the detector binary the harness probes for. It mirrors the
// production adapter's default (detect.defaultASTGrepBinary); kept here as a
// const so the skip-probe and the adapter agree on one name.
const astGrepBinary = "ast-grep"

// FixerFactory builds the engine.Fixer the harness drives a species' findings
// through. It receives the loaded manifest so it can branch on the fix kind /
// transform (a deterministic species reads m.Fix.Transform; an LLM species would
// return a recorded fixer keyed off m.Fix.Prompt). Returning the engine interface
// keeps the harness independent of any concrete adapter.
type FixerFactory func(m species.Manifest) (engine.Fixer, error)

// DeterministicFixer is the FixerFactory for deterministic species (ADR-0002):
// it builds the real fix.NewDeterministic for the manifest's transform, so the
// harness exercises the genuine delete-match path, not a stub. It is the default
// when Case.Fixer is nil.
func DeterministicFixer(m species.Manifest) (engine.Fixer, error) {
	if m.Fix.Kind != species.FixKindDeterministic {
		return nil, fmt.Errorf("fixture: DeterministicFixer used for non-deterministic species %q (fix kind %q)", m.Name, m.Fix.Kind)
	}
	return fix.NewDeterministic(m.Fix.Transform), nil
}

// Case describes one species fixture run. SpeciesDir is the on-disk species
// folder (its species.toml + detect.yml are loaded through the production
// loader/registry); RepoDir is the testdata repo seeded with the target smell;
// GoldenPath is the committed patch the produced diff must match. Fixer, when
// nil, defaults to DeterministicFixer — an LLM species sets it to a recorded
// fixer factory. Limits, when zero, defaults to verify.DefaultLimits().
type Case struct {
	Name       string
	SpeciesDir string
	RepoDir    string
	GoldenPath string
	Fixer      FixerFactory
	Limits     verify.Limits
}

// RunCase executes the full detect → fix → verify pipeline for one species over
// its testdata repo and asserts the produced patch against the golden:
//
//  1. Load the species manifest through the production loader+registry.
//  2. Build the species' real detector (ast-grep) and run it over the repo.
//  3. For each finding, build a FixTask and run the species' Fixer to get a diff.
//  4. Run the species' declared verifier gate (diff-bounded first, then the
//     manifest's checks — e.g. compile, detector-clears) against each diff; every
//     finding's fix MUST verify (a skip here is a harness failure, because the
//     fixture is authored to be fixable).
//  5. Concatenate the verified patches deterministically and diff against the
//     golden. UPDATE_GOLDEN=1 rewrites the golden; CI never auto-accepts.
//
// When ast-grep is absent the case is skipped (t.Skip) — detection is a plugin
// boundary, so CI without the binary stays green rather than failing.
func RunCase(t *testing.T, c Case) {
	t.Helper()

	if !astGrepAvailable() {
		t.Skipf("ast-grep not installed: skipping live %s fixture (detection is a plugin boundary, TECHSPEC §2)", c.Name)
	}

	// Resolve every path to absolute BEFORE changing the working directory:
	// the detector, the scratch-tree verifiers, and the golden read/write must
	// not depend on cwd once we chdir into the repo.
	speciesDir := mustAbs(t, c.SpeciesDir)
	repoDir := mustAbs(t, c.RepoDir)
	goldenPath := mustAbs(t, c.GoldenPath)

	// Run as a genuine `ant fix` from the repo root: chdir into the fixture repo
	// so ast-grep emits file paths relative to it (e.g. "repo.go") — the same
	// root-relative shape the scratch-tree verifiers and the deterministic
	// fixer's diff paths expect (a finding's File is relative to scope.Root, as
	// the colony/scout contract requires). t.Chdir restores cwd on cleanup.
	t.Chdir(repoDir)

	reg := species.NewRegistry()
	manifest := loadManifest(t, speciesDir, reg)

	detector := buildDetector(t, reg, manifest, speciesDir)
	scope := engine.Scope{Root: "."}

	findings, err := detector.Detect(context.Background(), scope)
	if err != nil {
		t.Fatalf("%s: detect over %s failed: %v", c.Name, c.RepoDir, err)
	}
	if len(findings) == 0 {
		t.Fatalf("%s: detector found no findings in %s — the fixture must contain the targeted smell", c.Name, repoDir)
	}
	sortFindings(findings)

	fixer := resolveFixer(t, c, manifest)
	limits := c.Limits
	if (limits == verify.Limits{}) {
		limits = verify.DefaultLimits()
	}

	patches := make([]string, 0, len(findings))
	for _, f := range findings {
		diff := runFix(t, c, fixer, f)
		runVerify(t, c, reg, manifest, detector, scope, limits, f, diff)
		patches = append(patches, concatPatch(diff))
	}

	got := []byte(strings.Join(patches, ""))
	assertGolden(t, c.Name, goldenPath, got)
}

// mustAbs resolves p to an absolute path, failing the test on error. Used to
// pin every case path before the harness chdirs into the fixture repo.
func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatalf("resolve absolute path for %q: %v", p, err)
	}
	return abs
}

// loadManifest reads and validates the species' species.toml through the same
// loader the resolver uses, so the harness asserts against the genuine manifest
// (a malformed one fails here, not silently).
func loadManifest(t *testing.T, speciesDir string, reg *species.Registry) species.Manifest {
	t.Helper()
	abs, err := filepath.Abs(speciesDir)
	if err != nil {
		t.Fatalf("resolve species dir %q: %v", speciesDir, err)
	}
	parent := filepath.Dir(abs)
	name := filepath.Base(abs)
	m, err := species.Load(os.DirFS(parent), name, "disk:"+abs, reg)
	if err != nil {
		t.Fatalf("load species manifest %s: %v", speciesDir, err)
	}
	return m
}

// buildDetector constructs the species' detector through the registry, with the
// rule path resolved to the on-disk species folder so the production ast-grep
// adapter reads the real detect.yml. This is the same Detector the CLI builds —
// the harness does not hand-roll detection.
func buildDetector(t *testing.T, reg *species.Registry, m species.Manifest, speciesDir string) engine.Detector {
	t.Helper()
	rulePath := filepath.Join(speciesDir, m.Detector.Rule)
	det, err := reg.Detector(m.Detector.Kind, m.Name, rulePath)
	if err != nil {
		t.Fatalf("%s: build detector (kind %q, rule %s): %v", m.Name, m.Detector.Kind, rulePath, err)
	}
	return det
}

// resolveFixer returns the case's Fixer (defaulting to DeterministicFixer) built
// for the manifest. It is the seam that lets the same harness drive a real
// deterministic fix or a recorded LLM fix.
func resolveFixer(t *testing.T, c Case, m species.Manifest) engine.Fixer {
	t.Helper()
	factory := c.Fixer
	if factory == nil {
		factory = DeterministicFixer
	}
	fixer, err := factory(m)
	if err != nil {
		t.Fatalf("%s: build fixer: %v", c.Name, err)
	}
	return fixer
}

// runFix builds the FixTask the colony would build for a finding and runs the
// species' Fixer, returning the proposed diff. A fixer error is a harness failure
// (the fixture is authored to be fixable).
func runFix(t *testing.T, c Case, fixer engine.Fixer, f engine.Finding) engine.ProposedDiff {
	t.Helper()
	task := buildFixTask(f)
	diff, err := fixer.Fix(context.Background(), task)
	if err != nil {
		t.Fatalf("%s: fix finding %s:%d failed: %v", c.Name, f.File, f.Span.StartLine, err)
	}
	if len(diff.Files) == 0 {
		t.Fatalf("%s: fixer produced an empty diff for %s:%d", c.Name, f.File, f.Span.StartLine)
	}
	return diff
}

// runVerify runs the species' declared verifier gate against a proposed diff and
// fails the test if the gate does not pass — the fixture's fix MUST clear compile
// and detector-clears (the genuine end-to-end proof). The gate is built from the
// manifest's [verify].checks in declared order, with diff-bounded prepended by
// verify.NewGate (TECHSPEC §8.1). detector-clears re-runs the SAME detector over
// the patched scratch tree.
func runVerify(t *testing.T, c Case, reg *species.Registry, m species.Manifest, detector engine.Detector, scope engine.Scope, limits verify.Limits, f engine.Finding, diff engine.ProposedDiff) {
	t.Helper()
	gate := buildGate(t, reg, m, detector, limits, f)
	res := gate.Verify(context.Background(), diff, scope)
	if !res.Passed {
		t.Fatalf("%s: verifier gate REJECTED the fix for %s:%d (the fixture must verify end-to-end): %s",
			c.Name, f.File, f.Span.StartLine, firstFailure(res))
	}
}

// buildGate composes the manifest's declared verifier checks into the same
// skip-and-surface gate the colony runs (verify.NewGate prepends diff-bounded).
// Only the M2 deterministic checks (compile, detector-clears) are wired here;
// tests:affected (M3) is recognized but routed to a clear "not wired in harness"
// failure so an M3 species author extends this one spot rather than re-rolling a
// gate.
func buildGate(t *testing.T, reg *species.Registry, m species.Manifest, detector engine.Detector, limits verify.Limits, f engine.Finding) engine.Verifier {
	t.Helper()
	rest := make([]engine.Verifier, 0, len(m.Verify.Checks))
	for _, check := range m.Verify.Checks {
		switch check {
		case verify.CheckCompile:
			rest = append(rest, verify.NewCompile(nil)) // real `go build ./...`
		case verify.CheckDetectorClears:
			rest = append(rest, verify.NewDetectorClears(detector, f))
		case verify.CheckDiffBounded:
			// diff-bounded is prepended by NewGate; skip a duplicate here.
		default:
			t.Fatalf("%s: verify check %q is declared but not wired in the fixture harness (extend buildGate for new checks)", m.Name, check)
		}
	}
	return verify.NewGate(limits, rest...)
}

// buildFixTask mirrors colony.buildFixTask: it seeds the FixTask's CodeContext
// from the finding's location and snippet so the deterministic fixer derives the
// delete-match diff exactly as it does in a live `ant fix` run (the colony's
// builder is unexported, so the one-struct shape is reproduced here).
func buildFixTask(f engine.Finding) engine.FixTask {
	return engine.FixTask{
		Finding: f,
		Context: engine.CodeContext{
			File:    f.File,
			Span:    f.Span,
			Snippet: f.Snippet,
		},
	}
}

// concatPatch renders a ProposedDiff's file patches into one stable string for the
// golden. File patches are ordered by path so the golden is deterministic
// regardless of the fixer's emission order.
func concatPatch(diff engine.ProposedDiff) string {
	files := make([]engine.FileDiff, len(diff.Files))
	copy(files, diff.Files)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	var b strings.Builder
	for _, fd := range files {
		b.WriteString(fd.Patch)
	}
	return b.String()
}

// assertGolden compares got against the committed golden, regenerating it when
// UPDATE_GOLDEN=1. CI never sets that variable, so a drift always fails the test
// (TECHSPEC §12 — goldens are the assertion, never auto-accepted).
func assertGolden(t *testing.T, name, goldenPath string, got []byte) {
	t.Helper()
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("%s: mkdir golden dir: %v", name, err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("%s: write golden %s: %v", name, goldenPath, err)
		}
		t.Logf("%s: updated golden %s", name, goldenPath)
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("%s: read golden %s (run with UPDATE_GOLDEN=1 to create): %v", name, goldenPath, err)
	}
	if string(got) != string(want) {
		t.Errorf("%s: produced patch drifted from the golden.\n--- got ---\n%s\n--- want ---\n%s\nRegenerate intentionally with UPDATE_GOLDEN=1 if this change is deliberate.",
			name, got, want)
	}
}

// firstFailure returns the detail of the first failed check in a VerifyResult,
// for a precise harness-failure message.
func firstFailure(res engine.VerifyResult) string {
	for _, ch := range res.Checks {
		if !ch.Passed {
			return fmt.Sprintf("%s: %s", ch.Name, ch.Detail)
		}
	}
	return "gate failed without a named check"
}

// sortFindings orders findings deterministically (file, line, col) so the
// concatenated golden patch is stable regardless of detector match order. It
// mirrors scout/colony sortFindings but without the species key (one species per
// fixture case).
func sortFindings(findings []engine.Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Span.StartLine != b.Span.StartLine {
			return a.Span.StartLine < b.Span.StartLine
		}
		return a.Span.StartCol < b.Span.StartCol
	})
}

// astGrepAvailable reports whether the ast-grep binary is resolvable on PATH.
// The harness probes once per case; a missing binary makes RunCase skip (CI-
// friendly) rather than fail.
func astGrepAvailable() bool {
	_, err := exec.LookPath(astGrepBinary)
	return err == nil
}
