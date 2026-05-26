package colony

import (
	"context"
	"fmt"
	"time"

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

		// SECURITY (Sprint 020): a command detector/verifier execs a SPECIES-SUPPLIED
		// script — the detector at SCAN time, a broader exec surface than the fix-time
		// tool runner. An untrusted (OriginUser, never-reviewed) community species must
		// NOT auto-execute its script before a human has reviewed it once. The trust
		// authority (species.ResolveTrust) computes ScriptExecAllowed per species; the
		// colony just reads it and threads it into both the detector and verifier
		// builders, so the policy stays in the single trust authority (trust.go).
		commandExec := d.ScriptExecAllowed

		det, err := buildDetector(m, rulesRoot, commandExec)
		if err != nil {
			return nil, nil, err
		}
		detectors = append(detectors, engine.NamedDetector{Species: m.Name, Detector: det})

		recipes[m.Name] = SpeciesRecipe{
			Fixer:       buildFixer(m, rc),
			NewVerifier: verifierBuilder(m, det, rulesRoot, rc.Limits, coverCache, commandExec),
			AutoApply:   d.EffectiveAutoApply, // FINAL trust decision (post freshly-installed override)
		}
	}
	return recipes, detectors, nil
}

// buildDetector constructs the species' detector. ast-grep is the default;
// command is the script escape hatch (Sprint 020). The rule/script path is
// resolved under rulesRoot: the manifest's Detector.Rule/Script is relative to
// the species FOLDER (e.g. "detect.yml" / "detect.sh"), and the materialized
// built-in tree lays files out as <rulesRoot>/<species>/<file>, so the species
// name is joined in between. With an empty rulesRoot the bare manifest-relative
// path is used (the recorded-fixture path needs no disk file).
//
// commandExec is the SCAN-TIME TRUST GATE (computed by species.ResolveTrust): an
// untrusted/freshly-installed user species is NOT allowed to exec its command
// script before review. When a command-kind species is denied, buildDetector
// returns a blockedDetector that surfaces a clear operational error on Detect
// (never silently runs the script, never silently produces zero findings).
func buildDetector(m species.Manifest, rulesRoot string, commandExec bool) (engine.Detector, error) {
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
	case species.DetectKindCommand:
		if !commandExec {
			// SECURITY: refuse to run an untrusted species' detector script.
			return blockedDetector{species: m.Name}, nil
		}
		script := d.Script
		if rulesRoot != "" && script != "" {
			script = rulesRoot + "/" + m.Name + "/" + script
		}
		interp := d.Interpreter
		if interp == "" {
			interp = species.DefaultScriptInterpreter
		}
		return detect.NewCommand(m.Name, interp, script), nil
	default:
		return nil, fmt.Errorf("%w: species %q detector kind %q not wired for fix", engine.ErrOperational, m.Name, d.Kind)
	}
}

// blockedDetector is the detector returned for a command-kind species whose
// scan-time script exec is denied by the trust gate (an unreviewed user species).
// It NEVER runs the script; Detect returns a clear operational error so the
// reason is visible (exit-code 2 classification) rather than a silent zero-finding
// run that would look like "no smells found".
type blockedDetector struct{ species string }

func (b blockedDetector) Detect(context.Context, engine.Scope) ([]engine.Finding, error) {
	return nil, fmt.Errorf("%w: species %q uses a command detector and is not yet trusted to run its script (review it once with `ant review` to allow scan-time exec)", engine.ErrOperational, b.species)
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
	case species.FixKindTool:
		// Tool-runner: exec the manifest-declared external formatter/autofixer on a
		// scratch copy and capture the diff (Sprint 017). Command + args are
		// declarative; the engine special-cases no tool. A malformed timeout string
		// is a build error surfaced as a visible skip (never a silent default).
		var timeout time.Duration
		if m.Fix.Timeout != "" {
			d, err := time.ParseDuration(m.Fix.Timeout)
			if err != nil {
				return unwiredFixer{species: m.Name, reason: fmt.Sprintf("invalid [fix].timeout %q: %v", m.Fix.Timeout, err)}
			}
			timeout = d
		}
		fixer, err := fix.NewTool(fix.ToolConfig{
			Command:     m.Fix.Command,
			Args:        m.Fix.Args,
			Timeout:     timeout,
			VersionArgs: m.Fix.VersionArgs,
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
// commandExec is the SCAN/VERIFY-TIME TRUST GATE (computed by species.ResolveTrust):
// an untrusted/freshly-installed user species is NOT allowed to exec its command:
// verifier script before review. When denied, a command: check is wired to a
// blockedVerifier that FAILS the gate with a clear reason (never runs the script),
// so the diff is skipped and the reason surfaces — a denied verifier must not
// silently pass the gate.
func verifierBuilder(m species.Manifest, det engine.Detector, rulesRoot string, limits verify.Limits, coverCache *testselect.ProfileCache, commandExec bool) func(engine.Finding) engine.Verifier {
	checks := m.Verify.Checks
	interp := m.Verify.Interpreter
	if interp == "" {
		interp = species.DefaultScriptInterpreter
	}
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
			case verify.CheckFormatterIdempotence:
				// Re-run the manifest-declared formatter over the post-fix tree and
				// assert no further changes (Sprint 017). The tool is declared in
				// [verify.tool]; a non-idempotent/oscillating formatter FAILS here (a
				// trust signal), never loops. nil runner → the real exec runner.
				rest = append(rest, verify.NewFormatterIdempotence(verify.ToolSpec{
					Command: m.Verify.Tool.Command,
					Args:    m.Verify.Tool.Args,
				}, nil))
			case "diff-bounded":
				// diff-bounded is always prepended by NewGate; skip an explicit one.
			default:
				// command:<script> escape hatch (Sprint 020): run the species-declared
				// verifier script on the scratch copy (install/parse/lint/compile gate).
				if script, ok := verify.ScriptFromCheck(c); ok {
					if !commandExec {
						rest = append(rest, blockedVerifier{name: c, species: m.Name})
						break
					}
					if rulesRoot != "" && script != "" {
						script = rulesRoot + "/" + m.Name + "/" + script
					}
					rest = append(rest, verify.NewCommandVerifier(c, interp, script))
				}
				// A genuinely unknown check (not command:*) is ignored, as before, so
				// the species still gets the gates it can run.
			}
		}
		return verify.NewGate(limits, rest...)
	}
}

// blockedVerifier is the verifier wired for a command: check whose script exec is
// denied by the trust gate (an unreviewed user species). It FAILS the gate with a
// clear reason — a denied verifier must skip the diff, never silently pass it.
type blockedVerifier struct {
	name    string
	species string
}

func (b blockedVerifier) Verify(context.Context, engine.ProposedDiff, engine.Scope) engine.VerifyResult {
	return engine.VerifyResult{
		Passed: false,
		Checks: []engine.CheckResult{{
			Name:   b.name,
			Passed: false,
			Detail: fmt.Sprintf("species %q uses a command verifier and is not yet trusted to run its script (review it once with `ant review` to allow exec)", b.species),
		}},
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
