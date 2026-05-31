package colony

import (
	"context"
	"sort"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/config"
	"github.com/gitpcl/ant/internal/engine/detect"
	"github.com/gitpcl/ant/internal/engine/species"
)

// resolveBuiltins resolves the full embedded built-in species set under the given
// config, the SAME path `ant scout` and `ant fix` use (Sprint 022). userRoot is
// empty so the test exercises only the embedded tree (no on-disk user species),
// keeping the assertion about which species are enabled-by-default stable.
func resolveBuiltins(t *testing.T, cfg config.Config) []species.Resolved {
	t.Helper()
	resolved, err := species.NewResolver("", nil).Resolve(cfg)
	if err != nil {
		t.Fatalf("resolve species: %v", err)
	}
	return resolved
}

// detectorSpecies returns the sorted set of species names a detector set covers.
func detectorSpecies(dets []engine.NamedDetector) []string {
	out := make([]string, 0, len(dets))
	for _, d := range dets {
		out = append(out, d.Species)
	}
	sort.Strings(out)
	return out
}

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// TestScoutDetectors_ParityWithBuiltinsFallback proves the manifest-driven scout
// detector set reproduces the legacy static detect.Builtins table for its two
// species (dead-code, unused-import) — i.e. the demoted fallback stays in parity
// with the resolver. ScoutDetectors is a SUPERSET (it sees the full manifest set),
// so parity is asserted as "every fallback species is present and resolves to the
// same ast-grep detector kind". This is the acceptance criterion: the static
// table is demoted to a fallback with a test proving parity with the resolver.
func TestScoutDetectors_ParityWithBuiltinsFallback(t *testing.T) {
	resolved := resolveBuiltins(t, config.Config{})
	scoutDets := ScoutDetectors(resolved, "")

	scoutNames := detectorSpecies(scoutDets)
	for _, fb := range detect.Builtins("") {
		if !contains(scoutNames, fb.Species) {
			t.Errorf("fallback species %q missing from ScoutDetectors; parity broken", fb.Species)
		}
	}

	// The fallback's two species are ast-grep; ScoutDetectors must build a
	// scan-safe ast-grep detector for them (never a blocked stand-in).
	for _, d := range scoutDets {
		if d.Species != "dead-code" && d.Species != "unused-import" {
			continue
		}
		safe, ok := d.Detector.(engine.ScanSafeDetector)
		if !ok || !safe.ScanSafe() {
			t.Errorf("species %q: ScoutDetectors built a non-scan-safe detector for an ast-grep species", d.Species)
		}
	}
}

// TestScoutDetectors_ThirdSpeciesAppears is the cheapest-validation test from the
// Sprint 022 Approach Validation Gate: enabling a species BEYOND
// dead-code/unused-import makes it appear in the scout detector set. The embedded
// tree ships ~30 enabled-by-default ast-grep species; this asserts a concrete
// third one (deep-nesting) is present, proving scout is no longer the static
// 2-species table.
func TestScoutDetectors_ThirdSpeciesAppears(t *testing.T) {
	resolved := resolveBuiltins(t, config.Config{})
	names := detectorSpecies(ScoutDetectors(resolved, ""))

	for _, want := range []string{"dead-code", "unused-import", "deep-nesting"} {
		if !contains(names, want) {
			t.Errorf("species %q not in scout detector set %v (manifest-driven scout regressed to a static table)", want, names)
		}
	}
	if len(names) <= 2 {
		t.Fatalf("scout detector set has only %d species %v; expected the full resolved manifest set", len(names), names)
	}
}

// TestScoutDetectors_DisableBuiltinViaConfig proves a built-in species disabled
// through ant.toml ([species.<name>] enabled=false) drops out of the scout
// detector set — the second half of the acceptance criterion.
func TestScoutDetectors_DisableBuiltinViaConfig(t *testing.T) {
	off := false
	cfg := config.Config{Species: map[string]config.Species{
		"deep-nesting": {Enabled: &off},
	}}
	resolved := resolveBuiltins(t, cfg)
	names := detectorSpecies(ScoutDetectors(resolved, ""))

	if contains(names, "deep-nesting") {
		t.Errorf("deep-nesting was disabled via ant.toml but still appears in scout detectors %v", names)
	}
	// A sibling default-enabled species is unaffected.
	if !contains(names, "dead-code") {
		t.Errorf("dead-code (still enabled) wrongly dropped from scout detectors %v", names)
	}
}

// TestScoutDetectors_EqualsFixSpeciesSet is the re-unification assertion: for the
// SAME resolved config, the species scout detects equal the species `ant fix`
// acts on (BuildRecipes). This is the core Sprint 022 deliverable — the two front
// doors resolve the identical species set.
func TestScoutDetectors_EqualsFixSpeciesSet(t *testing.T) {
	resolved := resolveBuiltins(t, config.Config{})

	// fix path: trust decisions wrap each resolved species (a no-op store would
	// require a real Store; here we build decisions directly carrying the resolved
	// EffectiveEnabled/EffectiveAutoApply, which is all BuildRecipes reads for the
	// species SET — script exec is irrelevant to membership, only to detector kind).
	decisions := make([]species.TrustDecision, 0, len(resolved))
	for _, r := range resolved {
		decisions = append(decisions, species.TrustDecision{
			Resolved:           r,
			EffectiveAutoApply: r.EffectiveAutoApply,
			ScriptExecAllowed:  true, // membership parity, not trust behavior, is under test here
		})
	}
	_, fixDetectors, err := BuildRecipes(decisions, nil, "", RecipeConfig{})
	if err != nil {
		t.Fatalf("BuildRecipes: %v", err)
	}

	scoutNames := detectorSpecies(ScoutDetectors(resolved, ""))
	fixNames := detectorSpecies(fixDetectors)

	if len(scoutNames) != len(fixNames) {
		t.Fatalf("scout species (%d) and fix species (%d) differ in count:\n scout=%v\n fix  =%v", len(scoutNames), len(fixNames), scoutNames, fixNames)
	}
	for i := range scoutNames {
		if scoutNames[i] != fixNames[i] {
			t.Errorf("scout/fix species set diverged at %d: scout=%q fix=%q", i, scoutNames[i], fixNames[i])
		}
	}
}

// TestScoutDetectors_CommandSpeciesBlockedNotDropped proves a command-detector
// species surfaces on the read-only scout path as a SCAN-SAFE blocked detector —
// visible (emits a finding), never silently dropped, and never running its script.
func TestScoutDetectors_CommandSpeciesBlockedNotDropped(t *testing.T) {
	resolved := []species.Resolved{{
		Manifest: species.Manifest{
			Name:     "needs-review-deps",
			Detector: species.Detect{Kind: species.DetectKindCommand, Script: "detect.sh"},
		},
		EffectiveEnabled: true,
		Origin:           species.OriginUser,
	}}
	dets := ScoutDetectors(resolved, "")
	if len(dets) != 1 {
		t.Fatalf("command species must NOT be dropped: got %d detectors, want 1", len(dets))
	}
	safe, ok := dets[0].Detector.(engine.ScanSafeDetector)
	if !ok || !safe.ScanSafe() {
		t.Fatal("a command species' scout detector must be scan-safe (it runs no script)")
	}
	findings, err := dets[0].Detector.Detect(context.Background(), engine.Scope{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("blocked scout detector must not error the run: %v", err)
	}
	if len(findings) != 1 || findings[0].Species != "needs-review-deps" {
		t.Fatalf("command species must surface a blocked finding (visible, not dropped); got %+v", findings)
	}
}
