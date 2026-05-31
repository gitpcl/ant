// Package doctor implements `ant doctor`: an environment-readiness probe that
// checks the external tools, model-endpoint configuration, git repository, and
// ant.toml the colony depends on, and reports whether the environment is ready
// to run (TECHSPEC §3 — all logic lives here; cmd/ant only parses + renders).
//
// The check set is derived from the SAME authorities the run path uses so
// doctor never drifts from reality: which external binaries are *required* comes
// from the resolved species' capability metadata (RequiresTool over the enabled
// set), the model env-var names mirror the rawmodel adapter wiring in
// cmd/ant/fix.go, the git probe reuses go-git (the apply path's repository
// authority), and ant.toml validity reuses config.LoadStrict. Nothing here
// hardcodes a parallel tool list when the metadata can supply it.
package doctor

import (
	"os"
	"sort"

	"github.com/gitpcl/ant/internal/engine/config"
	"github.com/gitpcl/ant/internal/engine/species"
)

// Status is a single check's outcome. The three-state model lets the report
// distinguish a hard failure (a REQUIRED capability is missing → non-zero exit)
// from an advisory warning (something optional is absent → exit stays zero) from
// a clean pass.
type Status string

const (
	// StatusOK — the check passed.
	StatusOK Status = "ok"
	// StatusWarn — advisory: something optional is absent or degraded. It does
	// NOT fail readiness (exit stays 0).
	StatusWarn Status = "warn"
	// StatusFail — a REQUIRED capability is missing. Readiness fails (exit 2).
	StatusFail Status = "fail"
)

// Check is one probe's result. Name is a stable machine token (snake_case),
// Detail is the human explanation, Required marks whether a failure of this
// check fails overall readiness, and Status is the outcome.
type Check struct {
	Name     string `json:"name"`
	Status   Status `json:"status"`
	Required bool   `json:"required"`
	Detail   string `json:"detail"`
}

// Report is the aggregate doctor result: every check plus the derived Ready
// flag. Ready is false iff any REQUIRED check failed — that is the only thing
// that drives the non-zero exit code (advisory warnings never do). The shape is
// the --json contract for CI integrations.
type Report struct {
	Ready  bool    `json:"ready"`
	Checks []Check `json:"checks"`
}

// Options carries the resolved inputs doctor needs. Root is the working-tree
// root (for the git probe). ConfigPath is the ant.toml path to validate.
// LookPath, Getenv, and OpenRepo are seams so tests can drive deterministic
// outcomes without a real PATH/env/repository; production wiring (Run) supplies
// the real implementations.
type Options struct {
	Root       string
	ConfigPath string
	// SpeciesUserRoot is the on-disk .ant/species directory used to resolve the
	// enabled species set whose capability metadata names the REQUIRED tools.
	SpeciesUserRoot string

	// Seams (nil → real implementation chosen by Run).
	LookPath func(string) (string, error)
	Getenv   func(string) string
	OpenRepo func(root string) error
}

// modelEnvEndpoint and modelEnvAPIKey mirror the env-var names the rawmodel
// adapter is wired from in cmd/ant/fix.go (RawModelEndpoint/RawModelAPIKey). They
// live here so doctor reports exactly what the fix path reads — change the fix
// wiring and this advisory check must move with it.
const (
	modelEnvEndpoint = "ANT_RAWMODEL_ENDPOINT"
	modelEnvAPIKey   = "ANT_RAWMODEL_API_KEY"
)

// advisoryTools are external binaries doctor reports on even when no enabled
// species currently requires them. They are the tools the documented species
// set can use (ast-grep for ast-grep detectors, goimports/ruff for tool fixes),
// so a user setting up CI sees their absence as a warning rather than a silent
// gap. A tool that an enabled species REQUIRES is promoted to a required check
// (see checkTools); these are the fallback advisory set.
var advisoryTools = []string{"ast-grep", "goimports", "ruff"}

// Diagnose runs every check against opts and aggregates them into a Report.
// Pure given its seams — it performs no I/O of its own beyond what the injected
// LookPath/Getenv/OpenRepo and config loader do — so the aggregation and the
// required-vs-advisory logic are unit-testable without touching the real
// environment.
func Diagnose(opts Options) Report {
	checks := make([]Check, 0, 8)
	checks = append(checks, checkConfig(opts)...)
	checks = append(checks, checkTools(opts)...)
	checks = append(checks, checkModelEnv(opts)...)
	checks = append(checks, checkGit(opts))

	ready := true
	for _, c := range checks {
		if c.Required && c.Status == StatusFail {
			ready = false
		}
	}
	return Report{Ready: ready, Checks: checks}
}

// checkConfig validates ant.toml through the shared loader (config.LoadStrict),
// reusing the one authority that owns parse + unknown-key handling. A missing
// file is OK — bare `ant` is zero-config — and surfaces as an advisory note. A
// malformed file is a REQUIRED failure: the run path would reject it (exit 2),
// so doctor must too. Unknown keys are advisory warnings, matching how scout/fix
// surface them on stderr without failing.
func checkConfig(opts Options) []Check {
	cfg, warnings, found, err := config.LoadStrict(opts.ConfigPath)
	_ = cfg
	if err != nil {
		return []Check{{
			Name:     "config",
			Status:   StatusFail,
			Required: true,
			Detail:   "ant.toml is invalid: " + err.Error(),
		}}
	}
	if !found {
		return []Check{{
			Name:     "config",
			Status:   StatusOK,
			Required: true,
			Detail:   "no ant.toml found (zero-config defaults apply)",
		}}
	}
	if len(warnings) > 0 {
		return []Check{{
			Name:     "config",
			Status:   StatusWarn,
			Required: true,
			Detail:   "ant.toml loaded with " + plural(len(warnings), "unknown key", "unknown keys") + ": " + joinWarnings(warnings),
		}}
	}
	return []Check{{
		Name:     "config",
		Status:   StatusOK,
		Required: true,
		Detail:   "ant.toml is valid",
	}}
}

// checkTools reports presence of every external binary the environment may need,
// deriving the REQUIRED set from capability metadata: a tool named by an ENABLED
// species' RequiresTool is required (its species cannot run without it); every
// other tool in the advisory set is advisory. This is the reuse mandate — the
// required list is read from the resolver's capability metadata, not a hardcoded
// parallel table. If species resolution fails (a malformed manifest), the
// advisory set still gets probed and a REQUIRED failure records the resolution
// error so doctor never silently drops the species-derived requirements.
func checkTools(opts Options) []Check {
	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	}

	required := map[string]bool{}
	var resolveCheck *Check
	resolved, err := species.NewResolver(opts.SpeciesUserRoot, nil).Resolve(loadConfigForResolve(opts))
	if err != nil {
		resolveCheck = &Check{
			Name:     "species",
			Status:   StatusFail,
			Required: true,
			Detail:   "cannot resolve species (capability metadata unreadable): " + err.Error(),
		}
	} else {
		for _, r := range resolved {
			if !r.EffectiveEnabled {
				continue
			}
			if tool := r.Capabilities().RequiresTool; tool != "" {
				required[tool] = true
			}
		}
	}

	// Probe the union of advisory tools and metadata-required tools, sorted for
	// stable output.
	names := map[string]bool{}
	for _, t := range advisoryTools {
		names[t] = true
	}
	for t := range required {
		names[t] = true
	}
	ordered := make([]string, 0, len(names))
	for t := range names {
		ordered = append(ordered, t)
	}
	sort.Strings(ordered)

	checks := make([]Check, 0, len(ordered)+1)
	for _, tool := range ordered {
		isRequired := required[tool]
		if _, lerr := lookPath(tool); lerr != nil {
			status := StatusWarn
			detail := tool + " not found on PATH (no enabled species requires it)"
			if isRequired {
				status = StatusFail
				detail = tool + " not found on PATH but an enabled species requires it"
			}
			checks = append(checks, Check{Name: "tool:" + tool, Status: status, Required: isRequired, Detail: detail})
			continue
		}
		checks = append(checks, Check{
			Name:     "tool:" + tool,
			Status:   StatusOK,
			Required: isRequired,
			Detail:   tool + " found on PATH",
		})
	}
	if resolveCheck != nil {
		checks = append(checks, *resolveCheck)
	}
	return checks
}

// loadConfigForResolve loads ant.toml for species resolution, swallowing a load
// error (checkConfig already reports config validity as its own check, so this
// path must not double-fail; on error it resolves against the zero config, which
// still yields the built-in species and their required tools).
func loadConfigForResolve(opts Options) config.Config {
	cfg, _, err := config.Load(opts.ConfigPath)
	if err != nil {
		return config.Config{}
	}
	return cfg
}

// checkModelEnv reports the rawmodel model-endpoint env vars. These are ADVISORY:
// only LLM species need them, and a project may run with none configured (the
// fix path simply skips LLM fixes when the endpoint is empty). A missing API key
// is not even a warning — local endpoints (Ollama/vLLM) need none — so it is an
// informational OK note when the endpoint is set.
func checkModelEnv(opts Options) []Check {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	endpoint := getenv(modelEnvEndpoint)
	if endpoint == "" {
		return []Check{{
			Name:     "model_endpoint",
			Status:   StatusWarn,
			Required: false,
			Detail:   modelEnvEndpoint + " is unset (LLM-fix species will be skipped)",
		}}
	}
	detail := modelEnvEndpoint + " is set"
	if getenv(modelEnvAPIKey) == "" {
		detail += " (" + modelEnvAPIKey + " unset — fine for local endpoints)"
	} else {
		detail += " with " + modelEnvAPIKey
	}
	return []Check{{
		Name:     "model_endpoint",
		Status:   StatusOK,
		Required: false,
		Detail:   detail,
	}}
}

// checkGit reports whether Root is a git repository, reusing the apply path's
// go-git PlainOpen authority via the OpenRepo seam. It is ADVISORY: scout and
// fix run read-only/staging without a repo; only `ant apply` (which lands on a
// branch) needs one, and apply reports its own clear error. Doctor flags the
// absence so a CI integrator knows apply will not work yet, without failing
// overall readiness.
func checkGit(opts Options) Check {
	openRepo := opts.OpenRepo
	if openRepo == nil {
		openRepo = func(string) error { return os.ErrNotExist }
	}
	if err := openRepo(opts.Root); err != nil {
		return Check{
			Name:     "git_repo",
			Status:   StatusWarn,
			Required: false,
			Detail:   "not a git repository (`ant apply` needs one; scout/fix do not)",
		}
	}
	return Check{
		Name:     "git_repo",
		Status:   StatusOK,
		Required: false,
		Detail:   "git repository detected",
	}
}
