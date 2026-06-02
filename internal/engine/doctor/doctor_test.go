package doctor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lookPathFrom builds a LookPath seam that "finds" exactly the named tools and
// reports os.ErrNotExist for everything else, so a test pins which binaries are
// on PATH without touching the real environment.
func lookPathFrom(present ...string) func(string) (string, error) {
	set := map[string]bool{}
	for _, p := range present {
		set[p] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "/usr/bin/" + name, nil
		}
		return "", os.ErrNotExist
	}
}

// envFrom builds a Getenv seam over a fixed map.
func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// baseOpts returns Options wired with deterministic seams: no config file
// (zero-config), no user species root, all seams injected. Individual tests
// override fields as needed.
func baseOpts(t *testing.T) Options {
	t.Helper()
	return Options{
		Root:            t.TempDir(),
		ConfigPath:      filepath.Join(t.TempDir(), "ant.toml"), // does not exist
		SpeciesUserRoot: "",                                     // built-ins only
		LookPath:        lookPathFrom("ast-grep", "goimports", "ruff", "gofmt", "pint", "prettier", "eslint"),
		Getenv:          envFrom(map[string]string{}),
		OpenRepo:        func(string) error { return nil }, // a repo
	}
}

func findCheck(r Report, name string) (Check, bool) {
	for _, c := range r.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

// TestDiagnoseReadyWhenAllPresent: with all tools present, a valid (absent)
// config, a git repo, and no LLM env needed, the report is Ready and every
// required check passes. The model-endpoint and git checks are advisory.
func TestDiagnoseReadyWhenAllPresent(t *testing.T) {
	r := Diagnose(baseOpts(t))
	if !r.Ready {
		t.Fatalf("expected Ready, got not ready: %+v", r.Checks)
	}
	for _, c := range r.Checks {
		if c.Required && c.Status == StatusFail {
			t.Errorf("required check %q failed unexpectedly: %s", c.Name, c.Detail)
		}
	}
	// Model endpoint absent → advisory warn, NOT a readiness failure.
	if mc, ok := findCheck(r, "model_endpoint"); !ok {
		t.Error("missing model_endpoint check")
	} else if mc.Required {
		t.Error("model_endpoint must be advisory, not required")
	} else if mc.Status != StatusWarn {
		t.Errorf("model_endpoint should warn when unset, got %s", mc.Status)
	}
}

// TestRequiredToolMissingFailsReadiness: a built-in ast-grep species requires
// ast-grep on PATH; removing it from PATH must flip that tool check to a
// REQUIRED failure and make the whole report not-ready. goimports/ruff absence
// (if not required by an enabled species) stays advisory.
func TestRequiredToolMissingFailsReadiness(t *testing.T) {
	opts := baseOpts(t)
	// Only ruff present; ast-grep + goimports absent.
	opts.LookPath = lookPathFrom("ruff")

	r := Diagnose(opts)

	astgrep, ok := findCheck(r, "tool:ast-grep")
	if !ok {
		t.Fatal("missing tool:ast-grep check")
	}
	if !astgrep.Required {
		t.Fatalf("ast-grep should be REQUIRED (built-in ast-grep species needs it); got advisory: %s", astgrep.Detail)
	}
	if astgrep.Status != StatusFail {
		t.Fatalf("ast-grep absent should FAIL, got %s", astgrep.Status)
	}
	if r.Ready {
		t.Fatal("report must be not-ready when a required tool is missing")
	}
}

// TestReadyMatchesRequiredFailureScan asserts the core aggregation invariant:
// Report.Ready is true iff NO required check failed. An absent tool that no
// enabled species requires must be a warn (advisory), and an absent advisory
// status must never flip Ready. We probe with only ast-grep present so several
// required tool-fix binaries (gofmt/goimports/ruff) are missing, then check the
// invariant holds and that any non-required tool failure is a warn, not a fail.
func TestReadyMatchesRequiredFailureScan(t *testing.T) {
	opts := baseOpts(t)
	opts.LookPath = lookPathFrom("ast-grep")

	r := Diagnose(opts)
	for _, c := range r.Checks {
		if strings.HasPrefix(c.Name, "tool:") && !c.Required && c.Status == StatusFail {
			t.Errorf("non-required tool %q failure should be warn, got fail", c.Name)
		}
	}
	wantReady := true
	for _, c := range r.Checks {
		if c.Required && c.Status == StatusFail {
			wantReady = false
		}
	}
	if r.Ready != wantReady {
		t.Errorf("Ready=%v but required-failure scan says %v", r.Ready, wantReady)
	}
}

// TestMalformedConfigFailsReadiness: an invalid ant.toml is a REQUIRED failure.
func TestMalformedConfigFailsReadiness(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ant.toml")
	if err := os.WriteFile(cfgPath, []byte("this is = = not toml ["), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := baseOpts(t)
	opts.ConfigPath = cfgPath

	r := Diagnose(opts)
	c, ok := findCheck(r, "config")
	if !ok {
		t.Fatal("missing config check")
	}
	if c.Status != StatusFail || !c.Required {
		t.Fatalf("malformed config must be a required failure, got status=%s required=%v", c.Status, c.Required)
	}
	if r.Ready {
		t.Fatal("malformed config must make the report not-ready")
	}
}

// TestModelEndpointSetIsOK: a configured endpoint flips the advisory check to OK.
func TestModelEndpointSetIsOK(t *testing.T) {
	opts := baseOpts(t)
	opts.Getenv = envFrom(map[string]string{modelEnvEndpoint: "http://localhost:11434/v1/chat/completions"})

	r := Diagnose(opts)
	c, _ := findCheck(r, "model_endpoint")
	if c.Status != StatusOK {
		t.Errorf("model_endpoint set should be OK, got %s", c.Status)
	}
	if c.Required {
		t.Error("model_endpoint must remain advisory even when set")
	}
}

// TestGitRepoAdvisory: no repo is an advisory warn, never a readiness failure.
func TestGitRepoAdvisory(t *testing.T) {
	opts := baseOpts(t)
	opts.OpenRepo = func(string) error { return os.ErrNotExist }

	r := Diagnose(opts)
	c, ok := findCheck(r, "git_repo")
	if !ok {
		t.Fatal("missing git_repo check")
	}
	if c.Required {
		t.Error("git_repo must be advisory")
	}
	if c.Status != StatusWarn {
		t.Errorf("absent repo should warn, got %s", c.Status)
	}
}

// TestJSONShape pins the --json report shape: a top-level object with a boolean
// "ready" and a "checks" array of {name,status,required,detail}. External CI
// adapters parse exactly this, so the field set + types are the contract.
func TestJSONShape(t *testing.T) {
	r := Diagnose(baseOpts(t))

	var buf bytes.Buffer
	if err := Render(&buf, FormatJSON, r); err != nil {
		t.Fatalf("render json: %v", err)
	}

	// Round-trips into a strict shape.
	var shape struct {
		Ready  *bool `json:"ready"`
		Checks []struct {
			Name     *string `json:"name"`
			Status   *string `json:"status"`
			Required *bool   `json:"required"`
			Detail   *string `json:"detail"`
		} `json:"checks"`
	}
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&shape); err != nil {
		t.Fatalf("doctor --json has unexpected shape: %v\n%s", err, buf.String())
	}
	if shape.Ready == nil {
		t.Error("missing top-level ready")
	}
	if len(shape.Checks) == 0 {
		t.Fatal("expected at least one check")
	}
	for i, c := range shape.Checks {
		if c.Name == nil || c.Status == nil || c.Required == nil || c.Detail == nil {
			t.Errorf("check[%d] missing a required field: %+v", i, c)
		}
		switch *c.Status {
		case string(StatusOK), string(StatusWarn), string(StatusFail):
		default:
			t.Errorf("check[%d] has unknown status %q", i, *c.Status)
		}
	}
}

// TestHumanRenderSummary asserts the human report ends with the readiness line
// matching the Ready flag, so the terminal reader and the JSON consumer agree.
func TestHumanRenderSummary(t *testing.T) {
	opts := baseOpts(t)
	opts.LookPath = lookPathFrom("ruff") // ast-grep missing → not ready
	r := Diagnose(opts)

	var buf bytes.Buffer
	if err := Render(&buf, FormatHuman, r); err != nil {
		t.Fatalf("render human: %v", err)
	}
	out := buf.String()
	if r.Ready {
		t.Fatal("expected not-ready for this fixture")
	}
	if !strings.Contains(out, "not ready") {
		t.Errorf("human report missing not-ready summary:\n%s", out)
	}
	if !strings.Contains(out, "FAIL") {
		t.Errorf("human report should mark the required failure with FAIL:\n%s", out)
	}
}
