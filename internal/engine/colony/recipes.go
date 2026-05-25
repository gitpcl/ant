package colony

import (
	"context"
	"fmt"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/detect"
	"github.com/gitpcl/ant/internal/engine/fix"
	"github.com/gitpcl/ant/internal/engine/species"
	"github.com/gitpcl/ant/internal/engine/verify"
	"github.com/gitpcl/ant/internal/engine/verify/testselect"
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

// BuildRecipes turns the trust-decided, enabled species into the driver's recipe
// map and the matching detector set, both honoring the --ant filter. It is the
// composition root for `ant fix`: it maps each species' manifest fix kind to a
// concrete Fixer (deterministic → fix.NewDeterministic; llm → fix.NewRawModel)
// and its declared verifier checks to a per-finding gate (diff-bounded first per
// TECHSPEC §8.1).
//
// CRITICAL — trust: the recipe's AutoApply is the FINAL effective trust from the
// single trust authority (species.ResolveTrust / EffectiveTrust), NOT the bare
// Sprint-004 config value. That decision already folds in the freshly-installed
// propose-only override (TECHSPEC §6.3), so a freshly-installed species cannot
// auto-land under --apply even if its manifest/ant.toml says auto_apply=true.
// The driver's fuseApply gates on exactly this AutoApply flag, so the override
// is enforced at the apply boundary by construction.
//
// rulesRoot is where ast-grep rule files resolve from (the embedded species
// tree, prepended to each manifest's rule path). A species whose fix kind cannot
// be wired (e.g. an llm species with no rawmodel endpoint configured) still gets
// a recipe whose Fixer returns a clear error, which the colony surfaces as a
// visible ant.skipped — never a silent drop (PRD §6.3).
func BuildRecipes(decisions []species.TrustDecision, antFilter []string, rulesRoot string, rc RecipeConfig) (map[string]SpeciesRecipe, []engine.NamedDetector, error) {
	allow := filterSet(antFilter)
	recipes := make(map[string]SpeciesRecipe)
	var detectors []engine.NamedDetector

	// One coverage cache PER RUN, shared by every ant's tests:affected verifier so
	// the (expensive) coverage profile is generated once and reused across ants,
	// regenerated only when the test-file set changes (TECHSPEC §5.3.1). Built here
	// in the composition root — the single place that runs once per `ant fix`.
	coverCache := testselect.NewProfileCache(testselect.NewGoProfileGenerator())

	for _, d := range decisions {
		r := d.Resolved
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
			NewVerifier: verifierBuilder(m, det, rc.Limits, coverCache),
			AutoApply:   d.EffectiveAutoApply, // FINAL trust decision (post freshly-installed override)
		}
	}
	return recipes, detectors, nil
}

// buildDetector constructs the species' detector. Only ast-grep is wired in v1
// (the command escape hatch lands later); the rule path is resolved under
// rulesRoot. The manifest's Detector.Rule is relative to the species FOLDER
// (e.g. "detect.yml"), and the materialized built-in tree lays rules out as
// <rulesRoot>/<species>/<rule> (mirroring detect.Builtins' "unused-import/detect.yml"
// table), so the species name is joined in between. With an empty rulesRoot the
// bare manifest-relative path is used (the recorded-fixture path needs no disk file).
func buildDetector(m species.Manifest, rulesRoot string) (engine.Detector, error) {
	d := m.Detector
	if d.Kind == "" {
		d = m.Detect // accept the [detect] alias
	}
	switch d.Kind {
	case species.DetectKindASTGrep, "":
		rule := d.Rule
		if rulesRoot != "" && rule != "" {
			rule = rulesRoot + "/" + m.Name + "/" + rule
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
// (TECHSPEC §8.1), then the manifest's declared checks (compile, detector-clears,
// tests:affected) in order. detector-clears is bound to the specific finding,
// which is why the gate is built per finding, not per species. The shared
// coverCache is passed to tests:affected so coverage is generated once per run and
// reused across every ant (TECHSPEC §5.3.1).
func verifierBuilder(m species.Manifest, det engine.Detector, limits verify.Limits, coverCache *testselect.ProfileCache) func(engine.Finding) engine.Verifier {
	checks := m.Verify.Checks
	return func(f engine.Finding) engine.Verifier {
		var rest []engine.Verifier
		for _, c := range checks {
			switch c {
			case "compile":
				rest = append(rest, verify.NewCompile(nil)) // nil → real `go build`
			case "detector-clears":
				rest = append(rest, verify.NewDetectorClears(det, f))
			case verify.CheckTestsAffected:
				// Smart test selection: coverage-map → import-graph → package
				// fallback, sharing the colony-wide coverage cache; runs ONLY the
				// affected tests and reports the strategy used (TECHSPEC §5.3.1).
				rest = append(rest, verify.NewTestsAffected(verify.AffectedConfig{Cache: coverCache}))
			case "diff-bounded":
				// diff-bounded is always prepended by NewGate; skip an explicit one.
			default:
				// command:* lands in a later sprint; ignore an unknown check here
				// rather than failing the whole run — the species still gets the
				// gates it can run today.
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
