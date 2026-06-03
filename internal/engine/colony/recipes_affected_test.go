package colony

import (
	"context"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/species"
	"github.com/gitpcl/ant/internal/engine/verify"
)

// TestBuildRecipesWiresTestsAffected proves a species declaring
// checks = ["tests:affected"] resolves through BuildRecipes into a gate that
// actually runs the tests:affected verifier and reports its strategy — i.e. the
// verifier is registered/wired, not silently ignored as it was before Sprint 010.
// It uses a doc-only change (no Go package), so the verifier degrades to "no
// affected tests" and passes WITHOUT a toolchain — the point here is that the
// tests:affected CheckResult appears in the gate output at all.
func TestBuildRecipesWiresTestsAffected(t *testing.T) {
	decisions := []species.TrustDecision{{
		Resolved: species.Resolved{
			Manifest: species.Manifest{
				Name:   "n+1-query",
				Detect: species.Detect{Kind: species.DetectKindASTGrep, Rule: "detect.yml"},
				Fix:    species.Fix{Kind: species.FixKindLLM, Prompt: "fix.md"},
				Verify: species.Verify{Checks: []string{"tests:affected"}},
			},
			EffectiveEnabled:   true,
			EffectiveAutoApply: false,
		},
		EffectiveAutoApply: false,
		// Trusted to execute repo test code: tests:affected runs _test.go (repo-
		// controlled), so the Sprint-026 exec gate (ScriptExecAllowed) must be true
		// for this wired-and-runs assertion — a vetted built-in species, which is
		// how an n+1-query species ships.
		ScriptExecAllowed: true,
	}}

	recipes, _, err := BuildRecipes(decisions, nil, "", RecipeConfig{Limits: verify.DefaultLimits()})
	if err != nil {
		t.Fatalf("BuildRecipes: %v", err)
	}
	recipe, ok := recipes["n+1-query"]
	if !ok {
		t.Fatal("expected a recipe for n+1-query")
	}

	// Use a Go file change so the wired tests:affected verifier dispatches to the
	// Go selectors and reports a strategy (Sprint 026 made tests:affected
	// per-language: a README/unknown-language change is now an honest skip, which
	// would not exercise the strategy-reporting path this test asserts).
	gate := recipe.NewVerifier(engine.Finding{Species: "n+1-query", File: "internal/x/x.go"})

	// A Go-file diff with no co-located test package degrades to the Go
	// package-fallback strategy — an honest pass — and the gate output must NAME
	// the tests:affected check AND report a strategy, proving it is wired into the
	// chain with the per-language Go dispatch intact.
	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "internal/x/x.go", Patch: "--- a/internal/x/x.go\n+++ b/internal/x/x.go\n@@ -1,0 +1,1 @@\n+// a new line\n"}},
		Fixer: "test",
	}
	res := gate.Verify(context.Background(), diff, engine.Scope{Root: t.TempDir()})

	var sawAffected bool
	for _, c := range res.Checks {
		if c.Name == verify.CheckTestsAffected {
			sawAffected = true
			if !strings.Contains(c.Detail, "tests:affected") && !strings.Contains(c.Detail, "coverage-map") &&
				!strings.Contains(c.Detail, "import-graph") && !strings.Contains(c.Detail, "package-fallback") {
				t.Errorf("tests:affected detail %q does not report a strategy", c.Detail)
			}
		}
	}
	if !sawAffected {
		t.Fatalf("tests:affected check was NOT in the gate output (not wired); checks=%+v", res.Checks)
	}
}
