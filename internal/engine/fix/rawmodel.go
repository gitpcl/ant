package fix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gitpcl/ant/internal/engine"
)

// RawModelConfig configures the rawmodel fixer. It is the seam through which the
// resolved ant.toml/flag values reach the adapter — the model id is ALWAYS a
// config value, never hardcoded (TECHSPEC §2). Endpoint and Model are required;
// APIKey, HTTPClient, and Timeout are optional.
type RawModelConfig struct {
	// Endpoint is the full OpenAI-compatible chat-completions URL (e.g.
	// http://localhost:11434/v1/chat/completions for Ollama, or a vLLM/OpenAI
	// host). Required — the adapter posts here.
	Endpoint string
	// Model is the model id sent in the request body. Required and never
	// defaulted to a literal: it flows from resolved config so swapping the
	// "small open-source model" stays a config change (TECHSPEC §2).
	Model string
	// APIKey, when non-empty, is sent as a Bearer token. Local models
	// (Ollama/vLLM) usually need none; hosted endpoints do.
	APIKey string
	// HTTPClient lets callers/tests inject a client (e.g. an httptest server's).
	// Nil uses a client with Timeout.
	HTTPClient *http.Client
	// Timeout bounds a single fix call so a hung endpoint becomes a skipped ant,
	// not a stalled colony (TECHSPEC §10). Zero uses a sane default.
	Timeout time.Duration
}

// defaultRawModelTimeout bounds one fix call when the config leaves Timeout zero.
const defaultRawModelTimeout = 60 * time.Second

// rawModelFixer is the provider-agnostic OpenAI-compatible HTTP Fixer
// (TECHSPEC §5.2). It posts exactly one localized FixTask as a chat completion
// and parses the model's reply into a ProposedDiff. It follows the harness
// adapter contract (TECHSPEC §10): one task per call, stateless, clean timeout.
type rawModelFixer struct {
	cfg    RawModelConfig
	client *http.Client
}

// compile-time assertion that rawModelFixer satisfies engine.Fixer.
var _ engine.Fixer = (*rawModelFixer)(nil)

// NewRawModel returns a rawmodel Fixer for the given config. It validates that
// the endpoint and model are present so a misconfigured fixer fails clearly at
// construction rather than producing a confusing HTTP error per finding.
func NewRawModel(cfg RawModelConfig) (engine.Fixer, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("fix: rawmodel requires a configured endpoint")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		// Never fall back to a hardcoded model — the model is a config value
		// (TECHSPEC §2). An empty model is a configuration error.
		return nil, fmt.Errorf("fix: rawmodel requires a configured model id (never hardcoded — TECHSPEC §2)")
	}
	client := cfg.HTTPClient
	if client == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = defaultRawModelTimeout
		}
		client = &http.Client{Timeout: timeout}
	}
	return &rawModelFixer{cfg: cfg, client: client}, nil
}

// chatRequest is the minimal OpenAI-compatible chat-completions request body.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse is the subset of the OpenAI-compatible response we read: the
// first choice's message content carries the proposed patch.
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Fix posts the FixTask to the configured endpoint and parses the reply into a
// ProposedDiff. Provenance is "rawmodel (<model>)" with the model from config,
// so review and --json show exactly which model produced the patch. The call is
// bound by the configured timeout (via the client and the request context); a
// timeout or transport error returns an error the colony turns into a skip
// (TECHSPEC §10), never a crash.
func (f *rawModelFixer) Fix(ctx context.Context, task engine.FixTask) (engine.ProposedDiff, error) {
	body, err := json.Marshal(chatRequest{
		Model:  f.cfg.Model,
		Stream: false,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: buildUserPrompt(task)},
		},
	})
	if err != nil {
		return engine.ProposedDiff{}, fmt.Errorf("fix: rawmodel encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return engine.ProposedDiff{}, fmt.Errorf("fix: rawmodel build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if f.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+f.cfg.APIKey)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return engine.ProposedDiff{}, fmt.Errorf("fix: rawmodel POST %s: %w", f.cfg.Endpoint, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return engine.ProposedDiff{}, fmt.Errorf("fix: rawmodel read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return engine.ProposedDiff{}, fmt.Errorf("fix: rawmodel endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return engine.ProposedDiff{}, fmt.Errorf("fix: rawmodel decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return engine.ProposedDiff{}, fmt.Errorf("fix: rawmodel response had no choices")
	}

	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	patch := extractPatch(content)
	if patch == "" {
		return engine.ProposedDiff{}, fmt.Errorf("fix: rawmodel response contained no usable diff")
	}

	path := task.Context.File
	if path == "" {
		path = task.Finding.File
	}

	return engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: path, Patch: patch}},
		// Provenance from config — the model id is the resolved value, not a
		// literal (TECHSPEC §2, §5.2).
		Fixer:     fmt.Sprintf("rawmodel (%s)", f.cfg.Model),
		Rationale: content,
	}, nil
}

const systemPrompt = "You are a code-fixing assistant. Return ONLY a unified diff that fixes the reported finding. Do not include prose."

// buildUserPrompt assembles the one-task message: the finding, its localized
// context, and any species prompt. Exactly one localized FixTask per call — no
// open-ended instruction (TECHSPEC §10 contract point 2).
func buildUserPrompt(task engine.FixTask) string {
	var b strings.Builder
	fmt.Fprintf(&b, "File: %s\n", task.Context.File)
	if task.Context.Language != "" {
		fmt.Fprintf(&b, "Language: %s\n", task.Context.Language)
	}
	fmt.Fprintf(&b, "Finding: %s\n", task.Finding.Message)
	fmt.Fprintf(&b, "Span: lines %d-%d\n", task.Finding.Span.StartLine, task.Finding.Span.EndLine)
	if task.Prompt != "" {
		fmt.Fprintf(&b, "Instructions:\n%s\n", task.Prompt)
	}
	if task.Context.Before != "" {
		fmt.Fprintf(&b, "Before:\n%s\n", task.Context.Before)
	}
	fmt.Fprintf(&b, "Code:\n%s\n", task.Context.Snippet)
	if task.Context.After != "" {
		fmt.Fprintf(&b, "After:\n%s\n", task.Context.After)
	}
	return b.String()
}

// extractPatch pulls a unified diff out of the model's reply. Models often wrap
// patches in a ```diff fenced block; we unwrap a single fenced block if present,
// otherwise return the trimmed content as-is. This is the minimal robustness the
// adapter needs without imposing a model-specific format.
func extractPatch(content string) string {
	const fence = "```"
	if !strings.Contains(content, fence) {
		return content
	}
	start := strings.Index(content, fence)
	rest := content[start+len(fence):]
	// Drop an optional language tag on the opening fence line (e.g. "diff\n").
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	}
	if end := strings.Index(rest, fence); end >= 0 {
		return strings.TrimRight(rest[:end], "\n") + "\n"
	}
	return strings.TrimSpace(rest)
}
