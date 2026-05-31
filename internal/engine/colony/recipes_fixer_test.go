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

// llmDecision builds a one-species enabled decision for an llm-kind species so a
// BuildRecipes run exercises buildLLMFixer's adapter selection.
func llmDecision(name string) []species.TrustDecision {
	return []species.TrustDecision{{
		Resolved: species.Resolved{
			Manifest: species.Manifest{
				Name:   name,
				Detect: species.Detect{Kind: species.DetectKindASTGrep, Rule: "detect.yml"},
				Fix:    species.Fix{Kind: species.FixKindLLM, Prompt: "fix.md"},
			},
			EffectiveEnabled:   true,
			EffectiveAutoApply: false,
		},
		EffectiveAutoApply: false,
	}}
}

// TestBuildFixerSelectsNamedAdapter proves Sprint 022 Finding 3: an llm species's
// effective fixer name (RecipeConfig.Fixer) selects the matching adapter from
// internal/engine/fix — pi/claudecode/codex/rawmodel — not always rawmodel.
//
// The deterministic, machine-independent signal is each adapter's own
// construction-time validation: with NO model id every adapter constructor fails
// with a message that uniquely names that adapter (pi/claudecode/codex/rawmodel).
// Because buildLLMFixer routes a construction failure through unwiredFixer, the
// per-finding Fix error carries that adapter-specific reason — proving the named
// constructor (and only it) was invoked for each fixer value. A live binary or
// network endpoint is therefore never required.
func TestBuildFixerSelectsNamedAdapter(t *testing.T) {
	cases := []struct {
		fixer      string
		wantReason string // substring uniquely identifying the selected adapter
	}{
		{FixerPi, "pi requires a configured model"},
		{FixerClaudeCode, "claudecode requires a configured model"},
		{FixerCodex, "codex requires a configured model"},
		{FixerRawModel, "rawmodel requires a configured"},
		{"", "pi requires a configured model"}, // empty defaults to pi (built-in default)
	}
	for _, tc := range cases {
		t.Run("fixer="+tc.fixer, func(t *testing.T) {
			// RawModelModel left empty so each selected adapter fails construction
			// with its own name; rawmodel also needs an endpoint to even reach the
			// model check, so give it one to prove the rawmodel branch is taken.
			rc := RecipeConfig{
				Limits:           verify.DefaultLimits(),
				Fixer:            tc.fixer,
				RawModelEndpoint: "http://example.invalid/v1/chat/completions",
			}
			recipes, _, err := BuildRecipes(llmDecision("n+1-query"), nil, "", rc)
			if err != nil {
				t.Fatalf("BuildRecipes(%q): unexpected error: %v", tc.fixer, err)
			}
			recipe, ok := recipes["n+1-query"]
			if !ok {
				t.Fatalf("no recipe built for fixer %q", tc.fixer)
			}
			_, ferr := recipe.Fixer.Fix(context.Background(), engine.FixTask{
				Finding: engine.Finding{Species: "n+1-query", File: "x.go"},
			})
			if ferr == nil {
				t.Fatalf("fixer %q: expected a construction-surfaced error, got nil", tc.fixer)
			}
			if !strings.Contains(ferr.Error(), tc.wantReason) {
				t.Fatalf("fixer %q: error %q does not name the expected adapter (want substring %q)",
					tc.fixer, ferr.Error(), tc.wantReason)
			}
		})
	}
}

// TestBuildRecipesRejectsReportOnly proves Sprint 022 Finding 4: a report-only
// species (fix.kind=none) reaching the fix front door is rejected with a clear,
// typed config error (engine.ErrOperational → exit 2) naming "report-only" and
// the species — it declares nothing to fix, so `ant fix` must reject it rather
// than build a no-op fixer or silently drop it. (The default `ant fix` never
// trips this because todo-expired ships disabled; this models an explicitly
// enabled / --ant-targeted report-only species.)
func TestBuildRecipesRejectsReportOnly(t *testing.T) {
	decisions := []species.TrustDecision{{
		Resolved: species.Resolved{
			Manifest: species.Manifest{
				Name:   "todo-expired",
				Detect: species.Detect{Kind: species.DetectKindASTGrep, Rule: "detect.yml"},
				Fix:    species.Fix{Kind: species.FixKindNone},
			},
			EffectiveEnabled: true,
		},
	}}
	recipes, _, err := BuildRecipes(decisions, nil, "", RecipeConfig{Limits: verify.DefaultLimits()})
	if err == nil {
		t.Fatalf("expected a rejection for a report-only species, got recipes=%v", recipes)
	}
	if !errors.Is(err, engine.ErrOperational) {
		t.Fatalf("report-only rejection must wrap engine.ErrOperational (exit 2), got: %v", err)
	}
	if !strings.Contains(err.Error(), "report-only") || !strings.Contains(err.Error(), "todo-expired") {
		t.Fatalf("error should name the report-only species; got: %v", err)
	}
}

// TestBuildFixerUnknownIsTypedConfigError proves an unrecognized fixer value is a
// TYPED config error (engine.ErrOperational → exit 2) returned from BuildRecipes,
// never a silent rawmodel fallback.
func TestBuildFixerUnknownIsTypedConfigError(t *testing.T) {
	rc := RecipeConfig{Limits: verify.DefaultLimits(), Fixer: "bogus-fixer"}
	recipes, _, err := BuildRecipes(llmDecision("n+1-query"), nil, "", rc)
	if err == nil {
		t.Fatalf("expected a typed config error for an unknown fixer, got recipes=%v", recipes)
	}
	if !errors.Is(err, engine.ErrOperational) {
		t.Fatalf("unknown fixer error must wrap engine.ErrOperational (exit 2), got: %v", err)
	}
	if !strings.Contains(err.Error(), "unknown fixer") || !strings.Contains(err.Error(), "bogus-fixer") {
		t.Fatalf("error should name the unknown fixer; got: %v", err)
	}
}
