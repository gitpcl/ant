package fix

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gitpcl/ant/internal/engine"
)

// defaultClaudeCodeBinary is the executable the adapter shells out to. Resolved
// from PATH at Fix time so a missing binary surfaces as a clean per-call skip.
const defaultClaudeCodeBinary = "claude"

// claudeCodeFixer execs Claude Code non-interactively in JSON print mode
// (`claude -p <prompt> --output-format json`) with exactly one localized FixTask
// and parses the structured result into a ProposedDiff (TECHSPEC §5.2, §10). It
// is stateless between tasks: each Fix is an independent print-mode invocation.
type claudeCodeFixer struct {
	cfg HarnessConfig
}

// compile-time assertion that claudeCodeFixer satisfies engine.Fixer.
var _ engine.Fixer = (*claudeCodeFixer)(nil)

// NewClaudeCode returns a Claude Code harness Fixer. It validates the model up
// front (never hardcoded — TECHSPEC §2); the binary is not probed until Fix runs.
func NewClaudeCode(cfg HarnessConfig) (engine.Fixer, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("fix: claudecode requires a configured model id (never hardcoded — TECHSPEC §2)")
	}
	if cfg.Binary == "" {
		cfg.Binary = defaultClaudeCodeBinary
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultHarnessTimeout
	}
	if cfg.runner == nil {
		cfg.runner = execRunner
	}
	return &claudeCodeFixer{cfg: cfg}, nil
}

// NewClaudeCodeWithRunner is NewClaudeCode with an injected command runner for
// recorded-response contract tests (no live `claude` binary — TECHSPEC §10, §12).
func NewClaudeCodeWithRunner(cfg HarnessConfig, runner CommandRunner) (engine.Fixer, error) {
	cfg.runner = runner
	return NewClaudeCode(cfg)
}

// Fix invokes `claude -p <one-task-prompt> --output-format json --model <model>`
// and parses the result envelope into a ProposedDiff. Provenance is
// "claudecode (<model>)" from config (TECHSPEC §2). Missing binary, non-zero
// exit, timeout, or parse failure returns a clean error → skip (TECHSPEC §10).
func (f *claudeCodeFixer) Fix(ctx context.Context, task engine.FixTask) (engine.ProposedDiff, error) {
	prompt := systemPrompt + "\n\n" + buildUserPrompt(task)
	return runHarness(ctx, harnessSpec{
		name:   "claudecode",
		model:  f.cfg.Model,
		binary: f.cfg.Binary,
		// Print mode (-p) with structured JSON output; --model selects the model
		// from config (never hardcoded). Claude Code reads the prompt from -p.
		args:    []string{"-p", prompt, "--output-format", "json", "--model", f.cfg.Model},
		timeout: f.cfg.Timeout,
		runner:  f.cfg.runner,
		parse:   parseClaudeCodeOutput,
	}, task)
}

// claudeCodeResult mirrors the single result object Claude Code emits with
// `--output-format json` (print mode): a top-level envelope whose `result` field
// carries the assistant's final text. is_error flags a failed run; subtype/
// session_id/cost/turn fields are present but unused here. This matches the
// documented Claude Code headless JSON output.
type claudeCodeResult struct {
	Type    string `json:"type"`    // "result"
	Subtype string `json:"subtype"` // e.g. "success" | "error_max_turns"
	IsError bool   `json:"is_error"`
	Result  string `json:"result"` // the assistant's final text — the patch source
}

// parseClaudeCodeOutput decodes the result envelope and returns the assistant
// text (rationale) and the same text as the patch source (extractPatch unwraps a
// fenced diff). An is_error envelope is a clean parse error → skip.
func parseClaudeCodeOutput(stdout []byte) (patch, rationale string, err error) {
	trimmed := strings.TrimSpace(string(stdout))
	if trimmed == "" {
		return "", "", fmt.Errorf("empty response")
	}
	var res claudeCodeResult
	if jerr := json.Unmarshal([]byte(trimmed), &res); jerr != nil {
		return "", "", fmt.Errorf("decode claudecode json: %w", jerr)
	}
	if res.IsError {
		return "", "", fmt.Errorf("claudecode reported an error (subtype %q)", res.Subtype)
	}
	if strings.TrimSpace(res.Result) == "" {
		return "", "", fmt.Errorf("claudecode result was empty")
	}
	return res.Result, res.Result, nil
}
