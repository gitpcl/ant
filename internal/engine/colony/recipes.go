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

// LLM fixer-adapter names (the effective config.Resolver.Fixer() value for an
// llm-kind species). Selection by these names lives HERE in the colony
// composition root, not in cmd/ant: the CLI only threads the resolved string
// (TECHSPEC §3). An llm species's manifest fix kind says "use an LLM"; this knob
// picks WHICH adapter — the harness binaries (pi/claudecode/codex) or the raw
// HTTP model — all of which already exist in internal/engine/fix.
const (
	FixerPi         = "pi"
	FixerClaudeCode = "claudecode"
	FixerCodex      = "codex"
	FixerRawModel   = "rawmodel"
)

// RecipeConfig carries the resolved colony knobs a recipe needs that are not on
// the species manifest: the diff-bounded limits (config layer), the effective
// fixer-adapter name for llm species (config.Resolver.Fixer()), and the rawmodel
// HTTP wiring (endpoint/model/api key from resolved config). Building recipes is
// composition the engine owns (not cmd/ant) so the CLI stays thin and free of the
// forbidden net/http import — the adapters live in the fix package.
type RecipeConfig struct {
	Limits verify.Limits
	// Fixer is the effective fixer-adapter name for llm species (pi | claudecode
	// | codex | rawmodel), resolved by config.Resolver.Fixer() (flag > ant.toml >
	// manifest > default). An empty value defaults to FixerPi (the built-in
	// default); an unrecognized value is a typed config error at build time, never
	// a silent rawmodel fallback.
	Fixer            string
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

		// Report-only species (fix kind none, Sprint 022 Finding 4) declare NOTHING
		// to fix: they belong on the scout/read-only path, not in a fix recipe. Reject
		// them here with a clear, typed config error (engine.ErrOperational → exit 2)
		// rather than silently dropping them or building a no-op fixer — `ant fix`
		// must tell the user the species is report-only and point at `ant scout`. This
		// runs BEFORE buildFixer so the message names report-only specifically instead
		// of falling through to the generic "fix kind not supported" skip.
		if m.IsReportOnly() {
			return nil, nil, fmt.Errorf("%w: species %q is report-only (fix.kind=%q) and declares nothing to fix — use `ant scout` to see its findings",
				engine.ErrOperational, m.Name, species.FixKindNone)
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

		fixer, err := buildFixer(m, rc)
		if err != nil {
			return nil, nil, err // typed config error (e.g. unknown fixer name) — exit 2
		}
		recipes[m.Name] = SpeciesRecipe{
			Fixer:       fixer,
			NewVerifier: verifierBuilder(m, det, rulesRoot, rc.Limits, coverCache, commandExec),
			AutoApply:   d.EffectiveAutoApply, // FINAL trust decision (post freshly-installed override)
		}
	}
	return recipes, detectors, nil
}

// ScoutDetectors builds the READ-ONLY scout detector set from the SAME resolved
// species set that drives BuildRecipes / `ant fix` (Sprint 022 Finding 1). It is
// the single composition that re-unifies the two front doors: scout no longer
// scans the hard-coded 2-species detect.Builtins table but every built-in,
// installed, and config-enabled species, honoring each species' EffectiveEnabled
// (a species disabled via ant.toml drops out of scout exactly as it drops out of
// fix). It lives here in the colony composition root — the existing single place
// that maps a resolved species to a concrete detector — so scout and fix share
// the identical species→detector mapping rather than two divergent tables.
//
// ast-grep species build the same NewASTGrep detector buildDetector does, with
// the rule resolved under rulesRoot as <rulesRoot>/<species>/<rule> (materialized
// embedded layout); the [detect] alias is collapsed to [detector] identically.
//
// SECURITY (Sprint 020/022): a `command` detector execs a species-supplied script
// at SCAN time — a broader surface than the fix-time tool runner — so scout MUST
// consult the per-species trust decision before running one. ScoutDetectors takes
// resolved TRUST DECISIONS (not bare resolved species) so it applies the SAME
// scan-time trust authority `ant fix` uses (species.ScriptExecAllowed): a vetted
// built-in or a reviewed installed species runs its REAL command detector (built
// with WithScanSafe so scout's invariant admits it), while an untrusted/never-
// reviewed user species surfaces as a scan-safe BLOCKED detector
// (detect.NewScoutBlocked) — visible in scout output, naming `ant fix`/`ant
// review` as the path that clears the gate, but running NO script. This makes
// built-in command species (unused-dependency, dead-config, hardcoded-secret, …)
// actually detectable by `ant scout`/CI while keeping unvetted third-party
// scripts off the read-only path (Sprint 022 Finding 1, partial follow-up).
func ScoutDetectors(decisions []species.TrustDecision, rulesRoot string) []engine.NamedDetector {
	out := make([]engine.NamedDetector, 0, len(decisions))
	for _, dec := range decisions {
		r := dec.Resolved
		if !r.EffectiveEnabled {
			continue // disabled species (ai-slop, or ant.toml enabled=false) do not scan
		}
		m := r.Manifest
		d := m.Detector
		if d.Kind == "" {
			d = m.Detect // accept the [detect] alias, exactly as buildDetector does
		}
		switch d.Kind {
		case species.DetectKindCommand:
			if !dec.ScriptExecAllowed {
				// Untrusted command species: visible but inert until reviewed.
				out = append(out, engine.NamedDetector{Species: m.Name, Detector: detect.NewScoutBlocked(m.Name)})
				continue
			}
			// Trusted (vetted built-in or reviewed installed): run the REAL command
			// detector, trust-marked (WithScanSafe) so scout's assertScanSafe admits it.
			out = append(out, engine.NamedDetector{
				Species:  m.Name,
				Detector: commandDetectorFor(m, d, rulesRoot, detect.WithScanSafe(true)),
			})
		default: // ast-grep is the default ("" or "ast-grep")
			out = append(out, engine.NamedDetector{Species: m.Name, Detector: detect.NewASTGrep(m.Name, rulesFile(rulesRoot, m.Name, d.Rule))})
		}
	}
	return out
}

// rulesFile resolves a species' rule/script FILE under rulesRoot. The manifest
// path is relative to the species FOLDER (e.g. "detect.yml" / "detect.sh") and
// the materialized built-in tree lays files out as <rulesRoot>/<species>/<file>,
// so the species name is joined in between. An empty rulesRoot or file yields the
// bare manifest-relative path (recorded fixtures need no disk file). It is the one
// place the scan and fix detector builders agree on this layout.
func rulesFile(rulesRoot, species, file string) string {
	if rulesRoot == "" || file == "" {
		return file
	}
	return rulesRoot + "/" + species + "/" + file
}

// commandDetectorFor builds a command (script escape-hatch) detector for m,
// resolving the script under rulesRoot and defaulting the interpreter. opts let
// the scout composition pass detect.WithScanSafe(true) for a trusted species; the
// fix path passes none. Shared by ScoutDetectors and buildDetector so the two
// front doors construct identical command detectors.
func commandDetectorFor(m species.Manifest, d species.Detect, rulesRoot string, opts ...detect.CommandOption) engine.Detector {
	interp := d.Interpreter
	if interp == "" {
		interp = species.DefaultScriptInterpreter
	}
	return detect.NewCommand(m.Name, interp, rulesFile(rulesRoot, m.Name, d.Script), opts...)
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
		return detect.NewASTGrep(m.Name, rulesFile(rulesRoot, m.Name, d.Rule)), nil
	case species.DetectKindCommand:
		if !commandExec {
			// SECURITY: refuse to run an untrusted species' detector script.
			return blockedDetector{species: m.Name}, nil
		}
		return commandDetectorFor(m, d, rulesRoot), nil
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
// the named transform with no model; llm selects the effective fixer ADAPTER
// (rc.Fixer: pi | claudecode | codex | rawmodel) — the harness binaries or the
// raw HTTP model — all wired from resolved config (the model is never hardcoded
// — TECHSPEC §2). A construction failure that is merely a missing knob (e.g. an
// llm species with no rawmodel endpoint) yields a Fixer that fails per finding (a
// visible skip). An UNRECOGNIZED fixer name is a typed config error
// (engine.ErrOperational, exit 2) returned to the caller — never a silent
// rawmodel fallback (Sprint 022 Finding 3).
func buildFixer(m species.Manifest, rc RecipeConfig) (engine.Fixer, error) {
	switch m.Fix.Kind {
	case species.FixKindDeterministic:
		transform := m.Fix.Transform
		if transform == "" {
			transform = fix.TransformDeleteMatch
		}
		return fix.NewDeterministic(transform), nil
	case species.FixKindLLM:
		return buildLLMFixer(m, rc)
	case species.FixKindTool:
		// Tool-runner: exec the manifest-declared external formatter/autofixer on a
		// scratch copy and capture the diff (Sprint 017). Command + args are
		// declarative; the engine special-cases no tool. A malformed timeout string
		// is a build error surfaced as a visible skip (never a silent default).
		var timeout time.Duration
		if m.Fix.Timeout != "" {
			d, err := time.ParseDuration(m.Fix.Timeout)
			if err != nil {
				return unwiredFixer{species: m.Name, reason: fmt.Sprintf("invalid [fix].timeout %q: %v", m.Fix.Timeout, err)}, nil
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
			return unwiredFixer{species: m.Name, reason: err.Error()}, nil
		}
		return fixer, nil
	default:
		return unwiredFixer{species: m.Name, reason: fmt.Sprintf("fix kind %q is not supported", m.Fix.Kind)}, nil
	}
}

// buildLLMFixer selects the concrete LLM adapter for an llm-kind species by the
// effective fixer name (rc.Fixer). An empty name defaults to FixerPi (the
// built-in config default). pi/claudecode/codex are the exec harnesses (model id
// from config, never hardcoded — TECHSPEC §2); rawmodel is the raw HTTP adapter
// wired from the resolved endpoint/model/api key. A construction failure that is
// only a missing runtime knob becomes a per-finding visible skip; an
// UNRECOGNIZED fixer name is a typed config error (engine.ErrOperational), so the
// front door fails with exit 2 instead of silently falling back to rawmodel.
func buildLLMFixer(m species.Manifest, rc RecipeConfig) (engine.Fixer, error) {
	name := rc.Fixer
	if name == "" {
		name = FixerPi
	}
	switch name {
	case FixerPi:
		fixer, err := fix.NewPi(fix.HarnessConfig{Model: rc.RawModelModel})
		return wrapUnwired(m, fixer, err), nil
	case FixerClaudeCode:
		fixer, err := fix.NewClaudeCode(fix.HarnessConfig{Model: rc.RawModelModel})
		return wrapUnwired(m, fixer, err), nil
	case FixerCodex:
		fixer, err := fix.NewCodex(fix.HarnessConfig{Model: rc.RawModelModel})
		return wrapUnwired(m, fixer, err), nil
	case FixerRawModel:
		fixer, err := fix.NewRawModel(fix.RawModelConfig{
			Endpoint: rc.RawModelEndpoint,
			Model:    rc.RawModelModel,
			APIKey:   rc.RawModelAPIKey,
		})
		return wrapUnwired(m, fixer, err), nil
	default:
		return nil, fmt.Errorf("%w: unknown fixer %q for species %q (expected one of: %s, %s, %s, %s)",
			engine.ErrOperational, name, m.Name, FixerPi, FixerClaudeCode, FixerCodex, FixerRawModel)
	}
}

// wrapUnwired turns an adapter constructor's (Fixer, error) into a single Fixer:
// on success the real adapter, on a construction error (e.g. a missing model id
// or rawmodel endpoint) an unwiredFixer that fails per finding as a VISIBLE skip
// — distinct from an unknown-fixer name, which is a typed config error and never
// reaches here.
func wrapUnwired(m species.Manifest, fixer engine.Fixer, err error) engine.Fixer {
	if err != nil {
		return unwiredFixer{species: m.Name, reason: err.Error()}
	}
	return fixer
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
