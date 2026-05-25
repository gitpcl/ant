package fix_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gitpcl/ant/internal/engine/fix"
)

// TestClaudeCodeParsesRecordedResponse asserts the claudecode adapter invokes
// `claude -p ... --output-format json --model <model>`, parses the result
// envelope's diff, and sets provenance from the config model — against a
// RECORDED response (no live binary).
func TestClaudeCodeParsesRecordedResponse(t *testing.T) {
	const wantModel = "qwen2.5-coder"
	recorded := loadFixture(t, "claudecode-response.json")

	var gotBinary string
	var gotArgs []string
	runner := func(_ context.Context, binary string, args []string, _ string) ([]byte, error) {
		gotBinary, gotArgs = binary, args
		return recorded, nil
	}

	fixer, err := fix.NewClaudeCodeWithRunner(fix.HarnessConfig{Model: wantModel, Timeout: time.Second}, runner)
	if err != nil {
		t.Fatalf("NewClaudeCode: %v", err)
	}
	diff, err := fixer.Fix(context.Background(), adapterTask())
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}

	if gotBinary != "claude" {
		t.Errorf("binary = %q, want claude", gotBinary)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "--output-format json") {
		t.Errorf("args missing --output-format json: %v", gotArgs)
	}
	if !strings.Contains(joined, "--model "+wantModel) {
		t.Errorf("args missing --model %s: %v", wantModel, gotArgs)
	}
	if !strings.Contains(joined, "1+N query in loop") {
		t.Errorf("localized task content not passed to claude: %v", gotArgs)
	}
	if len(diff.Files) != 1 || !strings.Contains(diff.Files[0].Patch, "db.GetAll(ids)") {
		t.Fatalf("parsed diff wrong: %+v", diff.Files)
	}
	if diff.Fixer != "claudecode ("+wantModel+")" {
		t.Errorf("provenance = %q, want %q", diff.Fixer, "claudecode ("+wantModel+")")
	}
}

// TestCodexParsesRecordedResponse asserts the codex adapter invokes
// `codex exec --json --model <model> <prompt>`, parses the final agent_message
// from the JSONL event stream, and sets provenance from the config model —
// against a RECORDED response (no live binary).
func TestCodexParsesRecordedResponse(t *testing.T) {
	const wantModel = "qwen2.5-coder"
	recorded := loadFixture(t, "codex-response.json")

	var gotBinary string
	var gotArgs []string
	runner := func(_ context.Context, binary string, args []string, _ string) ([]byte, error) {
		gotBinary, gotArgs = binary, args
		return recorded, nil
	}

	fixer, err := fix.NewCodexWithRunner(fix.HarnessConfig{Model: wantModel, Timeout: time.Second}, runner)
	if err != nil {
		t.Fatalf("NewCodex: %v", err)
	}
	diff, err := fixer.Fix(context.Background(), adapterTask())
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}

	if gotBinary != "codex" {
		t.Errorf("binary = %q, want codex", gotBinary)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.HasPrefix(joined, "exec ") {
		t.Errorf("codex must use the exec subcommand: %v", gotArgs)
	}
	if !strings.Contains(joined, "--json") {
		t.Errorf("args missing --json: %v", gotArgs)
	}
	if !strings.Contains(joined, "--model "+wantModel) {
		t.Errorf("args missing --model %s: %v", wantModel, gotArgs)
	}
	if len(diff.Files) != 1 || !strings.Contains(diff.Files[0].Patch, "db.GetAll(ids)") {
		t.Fatalf("parsed diff wrong: %+v", diff.Files)
	}
	if diff.Fixer != "codex ("+wantModel+")" {
		t.Errorf("provenance = %q, want %q", diff.Fixer, "codex ("+wantModel+")")
	}
}

// TestCodexPicksLastAgentMessage proves the codex parser skips reasoning items
// and earlier agent messages, taking the final agent_message text (statelessness
// of the stream parse). It uses an inline two-turn stream.
func TestCodexPicksLastAgentMessage(t *testing.T) {
	stream := `{"type":"item.completed","item":{"type":"agent_message","text":"first draft (ignore)"}}
{"type":"item.completed","item":{"type":"reasoning","text":"thinking"}}
{"type":"item.completed","item":{"type":"agent_message","text":"--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-a\n+b\n"}}`
	runner := func(_ context.Context, _ string, _ []string, _ string) ([]byte, error) {
		return []byte(stream), nil
	}
	fixer, err := fix.NewCodexWithRunner(fix.HarnessConfig{Model: "m", Timeout: time.Second}, runner)
	if err != nil {
		t.Fatalf("NewCodex: %v", err)
	}
	diff, err := fixer.Fix(context.Background(), adapterTask())
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !strings.Contains(diff.Files[0].Patch, "+b") || strings.Contains(diff.Files[0].Patch, "first draft") {
		t.Errorf("codex must take the LAST agent_message, got: %q", diff.Files[0].Patch)
	}
}
