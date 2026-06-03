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
	"github.com/gitpcl/ant/internal/engine/detect"
	"github.com/gitpcl/ant/internal/engine/fix"
	"github.com/gitpcl/ant/internal/engine/langmap"
	"github.com/gitpcl/ant/internal/engine/species"
	"github.com/gitpcl/ant/internal/engine/verify"
)

// astGrepBinary is the detector binary the harness probes for. It mirrors the
// production adapter's default (detect.defaultASTGrepBinary); kept here as a
// const so the skip-probe and the adapter agree on one name.
const astGrepBinary = "ast-grep"

// PlaceholderFile re-exports fix.PlaceholderFile so orchestration fixtures
// declare the tool-runner's {file} placeholder via the harness package they
// already import, without also importing the fix package. The tool-runner
// substitutes it with the scratch copy's path at fix time.
const PlaceholderFile = fix.PlaceholderFile

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

// RecordedFixer returns a FixerFactory for LLM-assisted species (ADR-0002:
// n+1-query, missing-await, nil-deref). It stands in for a live model in CI
// (TECHSPEC §10 — "LLM species use recorded fixer responses in tests; no live
// model in CI") by returning a fixer that always emits the SAME pre-authored
// unified-diff patch — the response a correctly-prompted model would produce for
// the fixture's single finding. The harness then drives that recorded patch
// through the REAL verifier gate (compile + tests:affected + detector-clears),
// so the proof is genuine: the recorded fix is only accepted if it actually
// compiles, passes the affected tests, and clears the detector. The model is
// stubbed; the safety gate is not.
//
// It validates the manifest is an llm species so a deterministic species can
// never be silently driven through a recorded LLM response (mirroring the guard
// in DeterministicFixer). patch is the file path + unified-diff body to apply.
func RecordedFixer(patch engine.FileDiff) FixerFactory {
	return RecordedFixerMulti(patch)
}

// RecordedFixerMulti is the MULTI-FILE variant of RecordedFixer: it records a
// ProposedDiff whose patch spans more than one file. A security remediation such
// as hardcoded-secret (Sprint 021) edits the SOURCE (replace the literal with an
// os.Getenv read) AND a config example (add the variable to .env.example) in one
// fix, so its recorded response is several FileDiffs. The harness drives the whole
// multi-file diff through the SAME real verifier gate (compile + the secret-
// scanner-clears command verifier + detector-clears) over a scratch copy, so the
// recorded fix is accepted only if every file applies, the project still builds,
// the scanner finds no secret, and the detector clears. RecordedFixer is the
// single-file convenience wrapper over this.
func RecordedFixerMulti(patches ...engine.FileDiff) FixerFactory {
	return func(m species.Manifest) (engine.Fixer, error) {
		if m.Fix.Kind != species.FixKindLLM {
			return nil, fmt.Errorf("fixture: RecordedFixer used for non-llm species %q (fix kind %q)", m.Name, m.Fix.Kind)
		}
		if len(patches) == 0 {
			return nil, fmt.Errorf("fixture: RecordedFixerMulti for %q needs at least one FileDiff", m.Name)
		}
		return &recordedFixer{species: m.Name, patches: patches}, nil
	}
}

// recordedFixer is the in-test stand-in for an LLM Fixer: it returns a fixed,
// pre-recorded ProposedDiff regardless of the task, so the harness exercises the
// detect→fix→verify→golden path without a network or a model. Provenance is
// marked "recorded (<species>)" so a golden/diff can never be mistaken for a
// live-model run. patches holds one entry per edited file (usually one; a
// multi-file remediation like hardcoded-secret records several).
type recordedFixer struct {
	species string
	patches []engine.FileDiff
}

// compile-time assertion that recordedFixer satisfies engine.Fixer.
var _ engine.Fixer = (*recordedFixer)(nil)

// Fix returns the recorded patch(es) as the proposed diff for the task. It is
// pure and stateless (the one-task adapter contract, TECHSPEC §10): every call
// yields the same recorded response, which the real verifier gate then accepts
// only if it genuinely compiles, passes affected tests / the command gate, and
// clears the detector.
func (f *recordedFixer) Fix(_ context.Context, _ engine.FixTask) (engine.ProposedDiff, error) {
	files := make([]engine.FileDiff, len(f.patches))
	copy(files, f.patches)
	return engine.ProposedDiff{
		Files: files,
		Fixer: fmt.Sprintf("recorded (%s)", f.species),
	}, nil
}

// Case describes one species fixture run. SpeciesDir is the on-disk species
// folder (its species.toml + detect.yml are loaded through the production
// loader/registry); RepoDir is the testdata repo seeded with the target smell;
// GoldenPath is the committed patch the produced diff must match. Fixer, when
// nil, defaults to DeterministicFixer — an LLM species sets it to a recorded
// fixer factory. Limits, when zero, defaults to verify.DefaultLimits().
//
// ToolCommand/ToolArgs OVERRIDE the manifest's tool command for the
// orchestration species (Sprint 017). The shipped species.toml names the REAL
// ecosystem tool (gofmt, prettier, ruff, eslint), but CI must not depend on it
// being installed, so a fixture points the tool-runner fix AND the
// formatter-idempotence verifier at a FAKE formatter on PATH instead. When set,
// the harness builds fix.NewTool and verify.NewFormatterIdempotence from these
// overrides; the manifest's declared command is still validated by the loader.
type Case struct {
	Name       string
	SpeciesDir string
	RepoDir    string
	GoldenPath string
	Fixer      FixerFactory
	Limits     verify.Limits

	// ToolCommand, when non-empty, overrides the [fix].command / [verify.tool].command
	// with a fake formatter for determinism (the orchestration species). ToolArgs
	// overrides the args ("{file}" is substituted by the tool-runner / idempotence
	// verifier exactly as in production).
	ToolCommand string
	ToolArgs    []string

	// RequiredTools names external binaries a command species' detect/verify
	// scripts need beyond its declared interpreter (e.g. "python3" for a config/
	// YAML-parse verifier). When any is absent the case SKIPS — mirroring the
	// ast-grep plugin-boundary skip — so CI without the tool stays green while the
	// gate runs for real where present. Empty for species whose scripts use only
	// universally-present tools (sh/awk/grep/sed/go).
	RequiredTools []string
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

	// Detection is a plugin boundary (TECHSPEC §2): an ast-grep species needs the
	// ast-grep binary, so CI without it SKIPS (stays green) rather than failing. A
	// command species (Sprint 020) depends only on its declared interpreter (sh),
	// which is universally present, so it never skips on the ast-grep probe.
	skipIfMatcherAbsent(t, c.Name, manifest)
	skipIfToolsAbsent(t, c.Name, c.RequiredTools)

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
		runVerify(t, c, speciesDir, reg, manifest, detector, scope, limits, f, diff)
		patches = append(patches, concatPatch(diff))
	}

	got := []byte(strings.Join(patches, ""))
	assertGolden(t, c.Name, goldenPath, got)
}

// RunDetectOnlyCase exercises a REPORT-ONLY species (Sprint 019: todo-expired):
// it runs the production detector over the fixture repo and asserts the
// report-only contract directly —
//
//  1. the detector produces findings (the species genuinely flags the smell), and
//  2. NO fix is produced and the working tree is BYTE-UNCHANGED before and after
//     (report-only means findings but no diff — the acceptance criterion).
//
// It deliberately does NOT build or run a Fixer: a report-only species proposes
// no change, so there is no diff and no golden. This is the harness counterpart
// to `ant scout`, which never writes the working tree (TECHSPEC §2). Like
// RunCase, it skips when ast-grep is absent (detection is a plugin boundary).
//
// expectMatches is the exact number of findings the fixture is authored to
// produce (so a rule that silently over- or under-matches fails here, not
// silently). It loads the real species.toml + detect.yml through the production
// loader/registry, exactly as RunCase does.
func RunDetectOnlyCase(t *testing.T, c Case, expectMatches int) {
	t.Helper()

	speciesDir := mustAbs(t, c.SpeciesDir)
	repoDir := mustAbs(t, c.RepoDir)
	t.Chdir(repoDir)

	reg := species.NewRegistry()
	manifest := loadManifest(t, speciesDir, reg)
	// ast-grep species skip when the matcher is absent; a command species never
	// does (it depends only on its interpreter). See skipIfMatcherAbsent.
	skipIfMatcherAbsent(t, c.Name, manifest)
	skipIfToolsAbsent(t, c.Name, c.RequiredTools)
	detector := buildDetector(t, reg, manifest, speciesDir)
	scope := engine.Scope{Root: "."}

	// Snapshot the working tree BEFORE detection so we can prove report-only
	// changes nothing (the report-only / no-diff contract).
	before := snapshotTree(t, ".")

	findings, err := detector.Detect(context.Background(), scope)
	if err != nil {
		t.Fatalf("%s: detect over %s failed: %v", c.Name, c.RepoDir, err)
	}
	if len(findings) != expectMatches {
		t.Fatalf("%s: detector produced %d findings, want %d (the report-only fixture must flag exactly the seeded stale markers)", c.Name, len(findings), expectMatches)
	}

	// The report-only contract: no fix, no diff, no working-tree change.
	after := snapshotTree(t, ".")
	if before != after {
		t.Fatalf("%s: report-only species modified the working tree — it must produce findings but NO diff", c.Name)
	}
}

// snapshotTree returns a stable digest of every regular file under root (path +
// content), so the detect-only harness can prove a report-only run leaves the
// working tree byte-identical. It mirrors the intent of scout's non-mutation
// snapshot test without depending on that internal helper.
func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		fmt.Fprintf(&b, "%s\x00%s\x00", path, data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot working tree under %q: %v", root, err)
	}
	return b.String()
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

// buildDetector constructs the species' detector with its rule/script resolved to
// the on-disk species folder, so the production adapter reads the real detect.yml
// (ast-grep) or runs the real detect.sh (command). This is the same Detector the
// CLI builds — the harness does not hand-roll detection. A command species is
// built DIRECTLY (detect.NewCommand) with the manifest's declared interpreter,
// because the registry constructor only knows the default interpreter; a fixture
// species is a vetted on-disk artifact (the harness's equivalent of OriginBuiltin),
// so the scan-time trust gate does not apply here.
func buildDetector(t *testing.T, reg *species.Registry, m species.Manifest, speciesDir string) engine.Detector {
	t.Helper()
	if m.Detector.Kind == species.DetectKindCommand {
		interp := m.Detector.Interpreter
		if interp == "" {
			interp = species.DefaultScriptInterpreter
		}
		scriptPath := filepath.Join(speciesDir, m.Detector.Script)
		return detect.NewCommand(m.Name, interp, scriptPath)
	}
	rulePath := filepath.Join(speciesDir, m.Detector.Rule)
	det, err := reg.Detector(m.Detector.Kind, m.Name, rulePath)
	if err != nil {
		t.Fatalf("%s: build detector (kind %q, rule %s): %v", m.Name, m.Detector.Kind, rulePath, err)
	}
	return det
}

// resolveFixer returns the case's Fixer built for the manifest. A tool species
// (ToolCommand set) builds fix.NewTool against the FAKE formatter override so the
// harness exercises the genuine tool-runner read→exec→diff path without a real
// formatter on PATH (Sprint 017). Otherwise it uses the case's Fixer (defaulting
// to DeterministicFixer), the seam that also drives a recorded LLM fix.
func resolveFixer(t *testing.T, c Case, m species.Manifest) engine.Fixer {
	t.Helper()
	if c.ToolCommand != "" {
		fixer, err := fix.NewTool(fix.ToolConfig{Command: c.ToolCommand, Args: c.ToolArgs})
		if err != nil {
			t.Fatalf("%s: build tool fixer (fake %q): %v", c.Name, c.ToolCommand, err)
		}
		return fixer
	}
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
func runVerify(t *testing.T, c Case, speciesDir string, reg *species.Registry, m species.Manifest, detector engine.Detector, scope engine.Scope, limits verify.Limits, f engine.Finding, diff engine.ProposedDiff) {
	t.Helper()
	gate := buildGate(t, c, speciesDir, reg, m, detector, limits, f)
	res := gate.Verify(context.Background(), diff, scope)
	if !res.Passed {
		t.Fatalf("%s: verifier gate REJECTED the fix for %s:%d (the fixture must verify end-to-end): %s",
			c.Name, f.File, f.Span.StartLine, firstFailure(res))
	}
}

// buildGate composes the manifest's declared verifier checks into the same
// skip-and-surface gate the colony runs (verify.NewGate prepends diff-bounded).
// The M2 deterministic checks (compile, detector-clears) and the M3 tests:affected
// check (the gate that makes propose-only LLM fixes safe — ADR-0002) are wired
// here against the REAL production verifiers, so an LLM species fixture proves the
// genuine detect→fix→verify path, not a stub gate.
func buildGate(t *testing.T, c Case, speciesDir string, reg *species.Registry, m species.Manifest, detector engine.Detector, limits verify.Limits, f engine.Finding) engine.Verifier {
	t.Helper()
	interp := m.Verify.Interpreter
	if interp == "" {
		interp = species.DefaultScriptInterpreter
	}
	rest := make([]engine.Verifier, 0, len(m.Verify.Checks))
	for _, check := range m.Verify.Checks {
		switch check {
		case verify.CheckCompile:
			rest = append(rest, verify.NewCompile(nil)) // real `go build ./...`
		case verify.CheckDetectorClears:
			rest = append(rest, verify.NewDetectorClears(detector, f))
		case verify.CheckFormatterIdempotence:
			// Re-run the formatter over the post-fix tree and assert no further
			// changes (Sprint 017). A tool fixture points this at the SAME fake
			// formatter the fix used (Case.ToolCommand) so a stable fake converges;
			// otherwise it uses the manifest's [verify.tool] (real ecosystem tool).
			cmd, args := m.Verify.Tool.Command, m.Verify.Tool.Args
			if c.ToolCommand != "" {
				cmd, args = c.ToolCommand, c.ToolArgs
			}
			rest = append(rest, verify.NewFormatterIdempotence(verify.ToolSpec{Command: cmd, Args: args}, nil))
		case verify.CheckTestsAffected:
			// The REAL tests:affected verifier (Sprint 010, TECHSPEC §5.3.1). A nil
			// cache omits the coverage-map strategy, so it degrades through
			// import-graph → package-fallback and runs ONLY the affected package's
			// tests in a scratch copy of the post-fix tree — no live model, no whole
			// suite. This is the gate that makes the LLM species' propose-only fixes
			// trustworthy (ADR-0002); the M3 LLM fixtures depend on it being wired to
			// the genuine verifier, not a stub.
			rest = append(rest, verify.NewTestsAffected(verify.AffectedConfig{}))
		case verify.CheckDiffBounded:
			// diff-bounded is prepended by NewGate; skip a duplicate here.
		default:
			// command:<script> escape hatch (Sprint 020): run the species-declared
			// verifier script on a scratch copy of the post-fix tree (install/parse/
			// lint/compile gate). Resolved to the on-disk species folder so the REAL
			// production verifier runs the REAL script — the fixture proves the
			// genuine detect→fix→command:verify path, not a stub. A fixture species is
			// vetted on-disk (the harness's OriginBuiltin equivalent), so the
			// scan-time trust gate does not apply here.
			if script, ok := verify.ScriptFromCheck(check); ok {
				scriptPath := filepath.Join(speciesDir, script)
				rest = append(rest, verify.NewCommandVerifier(check, interp, scriptPath))
				continue
			}
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
			File: f.File,
			// Mirror colony.buildFixTask: resolve the language from the file via the
			// single langmap authority so the fixture's FixTask matches a live run's
			// (Sprint 026).
			Language: langmap.LanguageForPath(f.File),
			Span:     f.Span,
			Snippet:  f.Snippet,
			// Mirror colony.buildFixTask: carry the verbatim source line(s) and any
			// ast-grep rewrite suggestion so the deterministic fixer's indented
			// delete-match and rewrite transforms patch lines that byte-match the
			// working tree.
			SourceLines: f.SourceLines,
			Replacement: f.Replacement,
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

// skipIfMatcherAbsent skips the case ONLY when the species needs an external
// matcher binary that is not installed. An ast-grep species (the default kind)
// depends on the ast-grep binary, which detection treats as a plugin boundary
// (TECHSPEC §2), so CI without it skips rather than fails. A command species
// (Sprint 020) depends only on the interpreter it declares (sh / bash / python3),
// which is universally present, so it is never skipped on the ast-grep probe —
// its detect/verify scripts run for real, hermetically. This keeps the deps
// species genuinely exercised in CI while preserving the existing ast-grep skip.
func skipIfMatcherAbsent(t *testing.T, name string, m species.Manifest) {
	t.Helper()
	kind := m.Detector.Kind
	if kind == "" {
		kind = m.Detect.Kind
	}
	if kind == species.DetectKindCommand {
		return // command species: no ast-grep dependency
	}
	if !astGrepAvailable() {
		t.Skipf("ast-grep not installed: skipping live %s fixture (detection is a plugin boundary, TECHSPEC §2)", name)
	}
}

// skipIfToolsAbsent skips the case when any external tool its scripts need (beyond
// the declared interpreter) is not on PATH — e.g. a config/YAML-parse verifier
// that calls python3. This keeps CI green on a host without the tool while the
// gate runs FOR REAL where present, mirroring the ast-grep plugin-boundary skip.
func skipIfToolsAbsent(t *testing.T, name string, tools []string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed: skipping live %s fixture (its command verifier needs %s; CI without it stays green)", tool, name, tool)
		}
	}
}
