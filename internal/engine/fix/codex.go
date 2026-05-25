package fix

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gitpcl/ant/internal/engine"
)

// defaultCodexBinary is the executable the adapter shells out to. Resolved from
// PATH at Fix time so a missing binary surfaces as a clean per-call skip.
const defaultCodexBinary = "codex"

// codexFixer execs Codex non-interactively in structured mode
// (`codex exec --json`) with exactly one localized FixTask and parses the
// JSONL event stream into a ProposedDiff (TECHSPEC §5.2, §10). It is stateless
// between tasks: each Fix is an independent `codex exec` invocation.
type codexFixer struct {
	cfg HarnessConfig
}

// compile-time assertion that codexFixer satisfies engine.Fixer.
var _ engine.Fixer = (*codexFixer)(nil)

// NewCodex returns a Codex harness Fixer. It validates the model up front (never
// hardcoded — TECHSPEC §2); the binary is not probed until Fix runs.
func NewCodex(cfg HarnessConfig) (engine.Fixer, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("fix: codex requires a configured model id (never hardcoded — TECHSPEC §2)")
	}
	if cfg.Binary == "" {
		cfg.Binary = defaultCodexBinary
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultHarnessTimeout
	}
	if cfg.runner == nil {
		cfg.runner = execRunner
	}
	return &codexFixer{cfg: cfg}, nil
}

// NewCodexWithRunner is NewCodex with an injected command runner for
// recorded-response contract tests (no live `codex` binary — TECHSPEC §10, §12).
func NewCodexWithRunner(cfg HarnessConfig, runner CommandRunner) (engine.Fixer, error) {
	cfg.runner = runner
	return NewCodex(cfg)
}

// Fix invokes `codex exec --json --model <model> <one-task-prompt>` and parses
// the JSONL event stream into a ProposedDiff. Provenance is "codex (<model>)"
// from config (TECHSPEC §2). Missing binary, non-zero exit, timeout, or parse
// failure returns a clean error → skip (TECHSPEC §10).
func (f *codexFixer) Fix(ctx context.Context, task engine.FixTask) (engine.ProposedDiff, error) {
	prompt := systemPrompt + "\n\n" + buildUserPrompt(task)
	return runHarness(ctx, harnessSpec{
		name:   "codex",
		model:  f.cfg.Model,
		binary: f.cfg.Binary,
		// `exec` is Codex's non-interactive subcommand; --json emits a JSONL
		// event stream; --model selects the config model (never hardcoded). The
		// one-task prompt is the positional argument.
		args:    []string{"exec", "--json", "--model", f.cfg.Model, prompt},
		timeout: f.cfg.Timeout,
		runner:  f.cfg.runner,
		parse:   parseCodexOutput,
	}, task)
}

// codexEvent is the subset of one Codex JSONL event the adapter reads. The final
// assistant text is carried by the last `item.completed` event whose nested item
// has type "agent_message"; its text is in item.text. This matches Codex's
// documented `codex exec --json` event stream (thread.started, turn.started,
// item.started, item.completed, turn.completed).
type codexEvent struct {
	Type string `json:"type"` // e.g. "item.completed"
	Item struct {
		Type string `json:"type"` // e.g. "agent_message"
		Text string `json:"text"`
	} `json:"item"`
}

// parseCodexOutput scans the JSONL event stream and returns the text of the last
// completed agent_message item (rationale + patch source). Non-JSON or unrelated
// lines are skipped (a tolerant stream reader). No agent_message is a clean parse
// error → skip.
func parseCodexOutput(stdout []byte) (patch, rationale string, err error) {
	if len(bytes.TrimSpace(stdout)) == 0 {
		return "", "", fmt.Errorf("empty response")
	}
	var text string
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	// Codex agent messages can be large; raise the line cap above the 64KB default.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue // skip blanks and any non-JSON banner lines
		}
		var ev codexEvent
		if jerr := json.Unmarshal(line, &ev); jerr != nil {
			continue // tolerate a malformed line within an otherwise valid stream
		}
		if ev.Type == "item.completed" && ev.Item.Type == "agent_message" && strings.TrimSpace(ev.Item.Text) != "" {
			text = ev.Item.Text // keep the last one (final turn wins)
		}
	}
	if serr := sc.Err(); serr != nil {
		return "", "", fmt.Errorf("scan codex stream: %w", serr)
	}
	if strings.TrimSpace(text) == "" {
		return "", "", fmt.Errorf("codex stream had no agent_message item")
	}
	return text, text, nil
}
