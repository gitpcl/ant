package fix_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/fix"
)

// loadFixture reads a recorded adapter response from testdata/adapters. CI runs
// against these recordings so no live model binary is needed (TECHSPEC §10, §12).
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	// Tests run with CWD at the package dir; fixtures live at the repo root.
	path := filepath.Join("..", "..", "..", "testdata", "adapters", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// TestPiParsesRecordedResponseAndSetsProvenance is the spike + acceptance test:
// against a RECORDED pi response (no live binary), the adapter parses the diff
// and sets provenance from the CONFIG model.
func TestPiParsesRecordedResponseAndSetsProvenance(t *testing.T) {
	const wantModel = "qwen2.5-coder"
	recorded := loadFixture(t, "pi-response.json")

	var gotBinary string
	var gotArgs []string
	var gotStdin string
	runner := func(_ context.Context, binary string, args []string, stdin string) ([]byte, error) {
		gotBinary, gotArgs, gotStdin = binary, args, stdin
		return recorded, nil
	}

	fixer, err := fix.NewPiWithRunner(fix.HarnessConfig{Model: wantModel}, runner)
	if err != nil {
		t.Fatalf("NewPi: %v", err)
	}

	diff, err := fixer.Fix(context.Background(), adapterTask())
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}

	// Invoked pi in --mode json with the config model (never hardcoded).
	if gotBinary != "pi" {
		t.Errorf("binary = %q, want pi", gotBinary)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "--mode json") {
		t.Errorf("args missing --mode json: %v", gotArgs)
	}
	if !strings.Contains(joined, "--model "+wantModel) {
		t.Errorf("args missing --model %s: %v", wantModel, gotArgs)
	}
	// The one localized task reached the harness (via -p prompt arg here).
	if !strings.Contains(joined, "1+N query in loop") && !strings.Contains(gotStdin, "1+N query in loop") {
		t.Errorf("localized task content not passed to pi")
	}

	if len(diff.Files) != 1 || diff.Files[0].Path != "svc/query.go" {
		t.Fatalf("file diff = %+v, want one diff for svc/query.go", diff.Files)
	}
	if !strings.Contains(diff.Files[0].Patch, "db.GetAll(ids)") {
		t.Errorf("parsed patch missing fix body:\n%s", diff.Files[0].Patch)
	}
	if diff.Fixer != "pi ("+wantModel+")" {
		t.Errorf("provenance = %q, want %q", diff.Fixer, "pi ("+wantModel+")")
	}
}

// adapterTask is the shared one-task fixture used by the harness adapter tests.
func adapterTask() engine.FixTask {
	return engine.FixTask{
		Finding: engine.Finding{
			Species:  "n+1-query",
			File:     "svc/query.go",
			Span:     engine.Span{StartLine: 10, EndLine: 14},
			Severity: engine.SeverityHigh,
			Message:  "1+N query in loop",
			Snippet:  "for _, id := range ids { db.Get(id) }",
		},
		Context: engine.CodeContext{
			File:     "svc/query.go",
			Language: "go",
			Span:     engine.Span{StartLine: 10, EndLine: 14},
			Snippet:  "for _, id := range ids { db.Get(id) }",
		},
		Prompt: "Batch the queries.",
	}
}
