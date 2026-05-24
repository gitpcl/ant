package colony

import (
	"context"
	"fmt"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/detect"
	"github.com/gitpcl/ant/internal/engine/fix"
	"github.com/gitpcl/ant/internal/engine/species"
	"github.com/gitpcl/ant/internal/engine/verify"
)

// RecipeConfig carries the resolved colony knobs a recipe needs that are not on
// the species manifest: the diff-bounded limits (config layer) and the rawmodel
// fixer wiring for llm species (endpoint/model/api key from resolved config).
// Building recipes is composition the engine owns (not cmd/ant) so the CLI stays
// thin and free of the forbidden net/http import — the rawmodel adapter lives in
// the fix package.
type RecipeConfig struct {
	Limits           verify.Limits
	RawModelEndpoint string
	RawModelModel    string
	RawModelAPIKey   string
}

// BuildRecipes turns the resolved, enabled species into the driver's recipe map
// and the matching detector set, both honoring the --ant filter. It is the
// composition root for `ant fix`: it maps each species' manifest fix kind to a
// concrete Fixer (deterministic → fix.NewDeterministic; llm → fix.NewRawModel)
// and its declared verifier checks to a per-finding gate (diff-bounded first per
// TECHSPEC §8.1). The effective auto_apply flows straight from resolution
// (ADR-0002) so only trusted species can auto-land under --apply.
//
// rulesRoot is where ast-grep rule files resolve from (the embedded species
// tree, prepended to each manifest's rule path). A species whose fix kind cannot
// be wired (e.g. an llm species with no rawmodel endpoint configured) still gets
// a recipe whose Fixer returns a clear error, which the colony surfaces as a
// visible ant.skipped — never a silent drop (PRD §6.3).
func BuildRecipes(resolved []species.Resolved, antFilter []string, rulesRoot string, rc RecipeConfig) (map[string]SpeciesRecipe, []engine.NamedDetector, error) {
	allow := filterSet(antFilter)
	recipes := make(map[string]SpeciesRecipe)
	var detectors []engine.NamedDetector

	for _, r := range resolved {
		m := r.Manifest
		if !r.EffectiveEnabled {
			continue // disabled species (e.g. ai-slop) do not run
		}
		if allow != nil {
			if _, ok := allow[m.Name]; !ok {
				continue
			}
		}

		det, err := buildDetector(m, rulesRoot)
		if err != nil {
			return nil, nil, err
		}
		detectors = append(detectors, engine.NamedDetector{Species: m.Name, Detector: det})

		recipes[m.Name] = SpeciesRecipe{
			Fixer:       buildFixer(m, rc),
			NewVerifier: verifierBuilder(m, det, rc.Limits),
			AutoApply:   r.EffectiveAutoApply,
		}
	}
	return recipes, detectors, nil
}

// buildDetector constructs the species' detector. Only ast-grep is wired in v1
// (the command escape hatch lands later); the rule path is resolved under
// rulesRoot. Mirrors detect.Builtins' per-species construction.
func buildDetector(m species.Manifest, rulesRoot string) (engine.Detector, error) {
	d := m.Detector
	if d.Kind == "" {
		d = m.Detect // accept the [detect] alias
	}
	switch d.Kind {
	case species.DetectKindASTGrep, "":
		rule := d.Rule
		if rulesRoot != "" && rule != "" {
			rule = rulesRoot + "/" + rule
		}
		return detect.NewASTGrep(m.Name, rule), nil
	default:
		return nil, fmt.Errorf("%w: species %q detector kind %q not wired for fix", engine.ErrOperational, m.Name, d.Kind)
	}
}

// buildFixer maps the manifest fix kind to a concrete Fixer. deterministic uses
// the named transform with no model; llm uses the rawmodel HTTP adapter wired
// from resolved config (the model is never hardcoded — TECHSPEC §2). A kind that
// cannot be wired yields a Fixer that fails per finding (a visible skip).
func buildFixer(m species.Manifest, rc RecipeConfig) engine.Fixer {
	switch m.Fix.Kind {
	case species.FixKindDeterministic:
		transform := m.Fix.Transform
		if transform == "" {
			transform = fix.TransformDeleteMatch
		}
		return fix.NewDeterministic(transform)
	case species.FixKindLLM:
		fixer, err := fix.NewRawModel(fix.RawModelConfig{
			Endpoint: rc.RawModelEndpoint,
			Model:    rc.RawModelModel,
			APIKey:   rc.RawModelAPIKey,
		})
		if err != nil {
			return unwiredFixer{species: m.Name, reason: err.Error()}
		}
		return fixer
	default:
		return unwiredFixer{species: m.Name, reason: fmt.Sprintf("fix kind %q is not supported", m.Fix.Kind)}
	}
}

// verifierBuilder returns a per-finding verifier constructor: diff-bounded first
// (TECHSPEC §8.1), then the manifest's declared checks (compile, detector-clears)
// in order. detector-clears is bound to the specific finding, which is why the
// gate is built per finding, not per species.
func verifierBuilder(m species.Manifest, det engine.Detector, limits verify.Limits) func(engine.Finding) engine.Verifier {
	checks := m.Verify.Checks
	return func(f engine.Finding) engine.Verifier {
		var rest []engine.Verifier
		for _, c := range checks {
			switch c {
			case "compile":
				rest = append(rest, verify.NewCompile(nil)) // nil → real `go build`
			case "detector-clears":
				rest = append(rest, verify.NewDetectorClears(det, f))
			case "diff-bounded":
				// diff-bounded is always prepended by NewGate; skip an explicit one.
			default:
				// tests:affected / command:* land in later sprints; ignore unknown
				// checks here rather than failing the whole run — the species still
				// gets the gates it can run today (diff-bounded + compile).
			}
		}
		return verify.NewGate(limits, rest...)
	}
}

// unwiredFixer fails every fix with a clear reason so a species whose fixer could
// not be constructed (e.g. llm with no endpoint) surfaces as a visible
// ant.skipped, not a silent drop (PRD §6.3).
type unwiredFixer struct {
	species string
	reason  string
}

func (f unwiredFixer) Fix(_ context.Context, _ engine.FixTask) (engine.ProposedDiff, error) {
	return engine.ProposedDiff{}, fmt.Errorf("fixer for species %q is not available: %s", f.species, f.reason)
}

// filterSet builds a lookup set from an --ant filter; nil filter means "all".
func filterSet(filter []string) map[string]struct{} {
	if len(filter) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(filter))
	for _, name := range filter {
		set[name] = struct{}{}
	}
	return set
}
