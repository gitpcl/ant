package colony

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/species"
	"github.com/gitpcl/ant/internal/engine/verify"
)

// commandSpeciesDecision builds a TrustDecision for a command-detector species
// with a command: verifier, parameterized by Origin and the resolved
// ScriptExecAllowed gate, so the tests drive the trust boundary directly.
func commandSpeciesDecision(name string, origin species.Origin, scriptExec bool) species.TrustDecision {
	return species.TrustDecision{
		Resolved: species.Resolved{
			Manifest: species.Manifest{
				Name:     name,
				Detector: species.Detect{Kind: species.DetectKindCommand, Script: "detect.sh"},
				Fix:      species.Fix{Kind: species.FixKindDeterministic, Transform: "delete-match"},
				Verify:   species.Verify{Checks: []string{"command:verify.sh"}},
			},
			Origin:           origin,
			EffectiveEnabled: true,
		},
		ScriptExecAllowed: scriptExec,
	}
}

// TestBuildRecipes_UntrustedCommandSpeciesBlocked is the SECURITY test: an
// untrusted (OriginUser, ScriptExecAllowed=false) command species must NOT run
// its detector or verifier script. The detector must error (a visible operational
// failure, never a silent zero-finding run); the verifier must FAIL the gate (a
// skip, never a silent pass). NO script is ever executed.
func TestBuildRecipes_UntrustedCommandSpeciesBlocked(t *testing.T) {
	decisions := []species.TrustDecision{
		commandSpeciesDecision("untrusted-deps", species.OriginUser, false),
	}
	recipes, detectors, err := BuildRecipes(decisions, nil, "", RecipeConfig{Limits: verify.DefaultLimits()})
	if err != nil {
		t.Fatalf("BuildRecipes: %v", err)
	}

	// Detector: must surface an operational error, not run the script.
	if len(detectors) != 1 {
		t.Fatalf("got %d detectors, want 1", len(detectors))
	}
	_, derr := detectors[0].Detector.Detect(context.Background(), engine.Scope{Root: t.TempDir()})
	if derr == nil {
		t.Fatal("untrusted command detector must return an error (blocked), not run the script and return findings")
	}
	if !errors.Is(derr, engine.ErrOperational) {
		t.Errorf("blocked detector error must classify as operational; got %v", derr)
	}
	if !strings.Contains(derr.Error(), "not yet trusted") {
		t.Errorf("blocked detector error should explain the trust gate; got %v", derr)
	}

	// Verifier: must fail the gate with a clear reason, not silently pass.
	recipe := recipes["untrusted-deps"]
	gate := recipe.NewVerifier(engine.Finding{Species: "untrusted-deps", File: "go.mod"})
	res := gate.Verify(context.Background(), engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "go.mod", Patch: "--- a/go.mod\n+++ b/go.mod\n@@ -1,1 +1,1 @@\n-module x\n+module y\n"}},
	}, engine.Scope{Root: t.TempDir()})
	if res.Passed {
		t.Fatal("untrusted command verifier must FAIL the gate (script blocked), not pass")
	}
	var sawBlocked bool
	for _, c := range res.Checks {
		if c.Name == "command:verify.sh" && !c.Passed && strings.Contains(c.Detail, "not yet trusted") {
			sawBlocked = true
		}
	}
	if !sawBlocked {
		t.Errorf("blocked verifier reason not surfaced in checks: %+v", res.Checks)
	}
}

// TestBuildRecipes_TrustedCommandSpeciesWired proves a trusted (built-in)
// command species resolves to a REAL command detector and a REAL command:
// verifier wired into the gate — the happy path the 4 species depend on.
func TestBuildRecipes_TrustedCommandSpeciesWired(t *testing.T) {
	decisions := []species.TrustDecision{
		commandSpeciesDecision("dead-config", species.OriginBuiltin, true),
	}
	recipes, detectors, err := BuildRecipes(decisions, nil, "", RecipeConfig{Limits: verify.DefaultLimits()})
	if err != nil {
		t.Fatalf("BuildRecipes: %v", err)
	}
	if len(detectors) != 1 {
		t.Fatalf("got %d detectors, want 1", len(detectors))
	}
	// The detector is a real command detector: with an empty rulesRoot and a
	// missing interpreter-less script it will try to exec `sh detect.sh`, which
	// fails (no such file) — but that proves it is the command adapter, not the
	// blocked stub (the blocked stub returns the "not yet trusted" message).
	_, derr := detectors[0].Detector.Detect(context.Background(), engine.Scope{Root: t.TempDir()})
	if derr != nil && strings.Contains(derr.Error(), "not yet trusted") {
		t.Fatal("trusted command species must NOT be blocked; got the trust-gate error")
	}

	// The verifier gate must NAME the command: check (it is wired, not ignored).
	gate := recipes["dead-config"].NewVerifier(engine.Finding{Species: "dead-config", File: "x.yml"})
	res := gate.Verify(context.Background(), engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "x.yml", Patch: "--- a/x.yml\n+++ b/x.yml\n@@ -1,1 +1,1 @@\n-a: 1\n+a: 2\n"}},
	}, engine.Scope{Root: t.TempDir()})
	var sawCommand bool
	for _, c := range res.Checks {
		if c.Name == "command:verify.sh" {
			sawCommand = true
			if strings.Contains(c.Detail, "not yet trusted") {
				t.Error("trusted command verifier must not be the blocked stub")
			}
		}
	}
	if !sawCommand {
		t.Errorf("command:verify.sh check was NOT wired into the gate; checks=%+v", res.Checks)
	}
}
