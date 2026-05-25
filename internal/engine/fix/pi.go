package fix

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gitpcl/ant/internal/engine"
)

// defaultPiBinary is the executable the adapter shells out to. Resolved from
// PATH at Fix time (never at construction) so a missing binary surfaces as a
// clean per-call skip, not a startup failure.
const defaultPiBinary = "pi"

// defaultHarnessTimeout bounds one harness invocation when config leaves Timeout
// zero. A bounded call is what makes a hung harness a skipped ant rather than a
// stalled colony (TECHSPEC §10 point 5). Shared by the three exec adapters.
const defaultHarnessTimeout = 120 * time.Second

// HarnessConfig configures an exec-based harness fixer (pi/claudecode/codex).
// It is the seam through which resolved ant.toml/flag values reach the adapter —
// the model id is ALWAYS a config value, never hardcoded (TECHSPEC §2). Model is
// required; Binary, Timeout, and the (unexported, test-only) runner are optional.
type HarnessConfig struct {
	// Model is the model id passed to the harness and shown in provenance.
	// Required and never defaulted to a literal: it flows from resolved config so
	// swapping the model stays a config change (TECHSPEC §2).
	Model string
	// Binary overrides the harness executable name/path (default per adapter).
	Binary string
	// Timeout bounds a single fix call so a hung harness becomes a skipped ant,
	// not a stalled colony (TECHSPEC §10). Zero uses defaultHarnessTimeout.
	Timeout time.Duration
	// runner is injected by tests to exercise the parse/timeout paths against a
	// RECORDED response with no live binary. Unexported so production always uses
	// execRunner.
	runner CommandRunner
}

// piFixer execs the Pi coding harness non-interactively in JSON mode
// (`pi -p <prompt> --mode json`) with exactly one localized FixTask and parses
// Pi's structured output into a ProposedDiff (TECHSPEC §5.2, §10). It is
// stateless between tasks: every Fix builds its own args and reads only cfg.
type piFixer struct {
	cfg HarnessConfig
}

// compile-time assertion that piFixer satisfies engine.Fixer (TECHSPEC §5.2).
var _ engine.Fixer = (*piFixer)(nil)

// NewPi returns a Pi harness Fixer for the given config. It validates the model
// up front so a misconfigured fixer fails clearly at construction rather than
// per finding. The binary is not probed until Fix runs.
func NewPi(cfg HarnessConfig) (engine.Fixer, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("fix: pi requires a configured model id (never hardcoded — TECHSPEC §2)")
	}
	if cfg.Binary == "" {
		cfg.Binary = defaultPiBinary
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultHarnessTimeout
	}
	if cfg.runner == nil {
		cfg.runner = execRunner
	}
	return &piFixer{cfg: cfg}, nil
}

// Fix invokes `pi -p <one-task-prompt> --mode json --model <model>` and parses
// the reply into a ProposedDiff. Provenance is "pi (<model>)" with the model
// from config (TECHSPEC §2). A missing binary, non-zero exit, timeout, or parse
// failure returns a clean error the colony turns into a skip (TECHSPEC §10) —
// never a crash.
func (f *piFixer) Fix(ctx context.Context, task engine.FixTask) (engine.ProposedDiff, error) {
	prompt := systemPrompt + "\n\n" + buildUserPrompt(task)
	return runHarness(ctx, harnessSpec{
		name:   "pi",
		model:  f.cfg.Model,
		binary: f.cfg.Binary,
		// Pi reads the prompt from -p and emits structured output with --mode
		// json; --model selects the model from config (never hardcoded).
		args:    []string{"-p", prompt, "--mode", "json", "--model", f.cfg.Model},
		timeout: f.cfg.Timeout,
		runner:  f.cfg.runner,
		parse:   parsePiOutput,
	}, task)
}

// NewPiWithRunner is NewPi with an injected command runner. It exists so
// contract tests exercise the parse/timeout paths against a RECORDED response
// with no live `pi` binary (TECHSPEC §10, §12). Production code uses NewPi, which
// always wires execRunner.
func NewPiWithRunner(cfg HarnessConfig, runner CommandRunner) (engine.Fixer, error) {
	cfg.runner = runner
	return NewPi(cfg)
}

// piResponse is the subset of Pi's `--mode json` output the adapter reads.
//
// ASSUMPTION (documented for the front-door teams): Pi's published `--mode json`
// schema was not reachable at implementation time. We assume a single JSON
// object with a top-level text field carrying the model's reply, tolerating the
// common alternative key names harnesses use ("content"/"response"/"output").
// The adapter reads the first non-empty of these. If Pi's real schema differs,
// only parsePiOutput changes — the contract (one task in, one ProposedDiff out,
// provenance, clean timeout) is unaffected. The recorded fixture
// testdata/adapters/pi-response.json documents the exact shape this expects.
type piResponse struct {
	Text     string `json:"text"`
	Content  string `json:"content"`
	Response string `json:"response"`
	Output   string `json:"output"`
}

// parsePiOutput decodes Pi's JSON reply and returns the model text (rationale)
// and the same text as the patch source (extractPatch unwraps a fenced diff).
func parsePiOutput(stdout []byte) (patch, rationale string, err error) {
	trimmed := strings.TrimSpace(string(stdout))
	if trimmed == "" {
		return "", "", fmt.Errorf("empty response")
	}
	var resp piResponse
	if jerr := json.Unmarshal([]byte(trimmed), &resp); jerr != nil {
		return "", "", fmt.Errorf("decode pi json: %w", jerr)
	}
	text := firstNonEmpty(resp.Text, resp.Content, resp.Response, resp.Output)
	if text == "" {
		return "", "", fmt.Errorf("pi response carried no text field")
	}
	return text, text, nil
}

// firstNonEmpty returns the first argument whose trimmed value is non-empty.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
