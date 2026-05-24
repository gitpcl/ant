package detect

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
)

// recordedOutput loads the committed ast-grep JSON fixture so the parse path is
// exercised without a live ast-grep binary (TECHSPEC §12 — adapter tests run
// against a recorded response; CI needs no installed matcher).
func recordedOutput(t *testing.T) []byte {
	t.Helper()
	// internal/engine/detect → repo root is three levels up.
	path := filepath.Join("..", "..", "..", "testdata", "astgrep-output.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recorded ast-grep output: %v", err)
	}
	return data
}

func TestASTGrepParsesRecordedMatches(t *testing.T) {
	out := recordedOutput(t)
	runner := func(_ context.Context, _ string, _ []string) ([]byte, error) {
		return out, nil
	}
	det := NewASTGrep("unused-import", "detect.yml", withRunner(runner))

	findings, err := det.Detect(context.Background(), engine.Scope{Root: "."})
	if err != nil {
		t.Fatalf("Detect: unexpected error: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2", len(findings))
	}

	first := findings[0]
	if first.Species != "unused-import" {
		t.Errorf("Species = %q, want %q (owned by the adapter, not the match)", first.Species, "unused-import")
	}
	if first.File != "main.go" {
		t.Errorf("File = %q, want %q", first.File, "main.go")
	}
	// ast-grep is 0-based; Ant Spans are 1-based. Fixture start line 2 → 3.
	wantSpan := engine.Span{StartLine: 3, StartCol: 1, EndLine: 3, EndCol: 17}
	if first.Span != wantSpan {
		t.Errorf("Span = %+v, want %+v (0-based ast-grep must convert to 1-based)", first.Span, wantSpan)
	}
	if first.Severity != engine.SeverityHigh {
		t.Errorf("Severity = %v, want high (ast-grep \"error\" maps to high)", first.Severity)
	}
	if first.Message == "" {
		t.Error("Message is empty; want the ast-grep message text")
	}
	if first.Snippet != "import \"strings\"" {
		t.Errorf("Snippet = %q, want the matched text", first.Snippet)
	}
	if first.Meta["ruleId"] != "unused-import" {
		t.Errorf("Meta[ruleId] = %q, want %q", first.Meta["ruleId"], "unused-import")
	}

	// The warning-severity match maps to medium.
	if findings[1].Severity != engine.SeverityMedium {
		t.Errorf("second finding Severity = %v, want medium (ast-grep \"warning\")", findings[1].Severity)
	}
}

func TestASTGrepEmptyOutputIsNoFindings(t *testing.T) {
	for _, payload := range [][]byte{nil, []byte("  \n"), []byte("[]")} {
		runner := func(_ context.Context, _ string, _ []string) ([]byte, error) {
			return payload, nil
		}
		det := NewASTGrep("x", "detect.yml", withRunner(runner))
		findings, err := det.Detect(context.Background(), engine.Scope{Root: "."})
		if err != nil {
			t.Fatalf("Detect(%q): unexpected error: %v", payload, err)
		}
		if len(findings) != 0 {
			t.Errorf("Detect(%q): got %d findings, want 0", payload, len(findings))
		}
		if findings == nil {
			t.Errorf("Detect(%q): findings is nil, want empty slice", payload)
		}
	}
}

// TestASTGrepMissingBinaryIsOperational is the contract-critical test: when the
// ast-grep binary is absent, Detect returns a typed
// *engine.DetectorUnavailableError that classifies as exit code 2 — it does NOT
// panic or crash. This runs the same whether or not ast-grep is installed
// locally, because the runner is injected to simulate the not-found error.
func TestASTGrepMissingBinaryIsOperational(t *testing.T) {
	notFound := &exec.Error{Name: "ast-grep", Err: exec.ErrNotFound}
	runner := func(_ context.Context, _ string, _ []string) ([]byte, error) {
		return nil, notFound
	}
	det := NewASTGrep("unused-import", "detect.yml", withRunner(runner))

	findings, err := det.Detect(context.Background(), engine.Scope{Root: "."})
	if findings != nil {
		t.Errorf("findings = %v, want nil on a missing binary", findings)
	}
	if err == nil {
		t.Fatal("Detect returned nil error for a missing binary; want operational error")
	}
	var unavailable *engine.DetectorUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("error type = %T, want *engine.DetectorUnavailableError", err)
	}
	if !errors.Is(err, engine.ErrOperational) {
		t.Error("missing-binary error must classify as engine.ErrOperational (exit code 2)")
	}
	if engine.ExitCode(err) != engine.ExitOperational {
		t.Errorf("ExitCode = %d, want %d (operational)", engine.ExitCode(err), engine.ExitOperational)
	}
}

// TestASTGrepRealBinaryNotFound exercises the actual exec path (no injected
// runner) so the production execRunner's not-found detection is covered even
// when ast-grep is not installed. It uses an executable name guaranteed not to
// exist.
func TestASTGrepRealBinaryNotFound(t *testing.T) {
	det := NewASTGrep("x", "detect.yml", WithBinary("ant-no-such-detector-binary"))
	_, err := det.Detect(context.Background(), engine.Scope{Root: "."})
	if err == nil {
		t.Fatal("want an error for a non-existent binary")
	}
	if !errors.Is(err, engine.ErrOperational) {
		t.Errorf("error %v must classify as operational (exit 2)", err)
	}
}
