package detect

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/gitpcl/ant/internal/engine"
)

// TestCommandParsesRecordedFindings exercises the parse path against a recorded
// script payload (no live interpreter) — the command detector's JSON contract
// maps onto the SAME engine.Finding the ast-grep adapter produces.
func TestCommandParsesRecordedFindings(t *testing.T) {
	const payload = `[
	  {"file":"go.mod","line":7,"endLine":7,"col":2,"endCol":20,"severity":"medium",
	   "message":"dependency \"example.com/unused\" is declared but never imported",
	   "snippet":"example.com/unused v1.2.3","sourceLine":"\texample.com/unused v1.2.3",
	   "ruleId":"unused-dependency"},
	  {"file":"go.mod","line":8,"message":"second finding, minimal fields"}
	]`
	var gotArgs []string
	runner := func(_ context.Context, _ string, args []string) ([]byte, error) {
		gotArgs = args
		return []byte(payload), nil
	}
	det := NewCommand("unused-dependency", "sh", "detect.sh", WithCommandRunner(runner))

	findings, err := det.Detect(context.Background(), engine.Scope{Root: "myrepo"})
	if err != nil {
		t.Fatalf("Detect: unexpected error: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2", len(findings))
	}

	// argv form: script path then scope root — NO shell string.
	if len(gotArgs) != 2 || gotArgs[0] != "detect.sh" || gotArgs[1] != "myrepo" {
		t.Errorf("args = %v, want [detect.sh myrepo] (argv form, scope root passed positionally)", gotArgs)
	}

	first := findings[0]
	if first.Species != "unused-dependency" {
		t.Errorf("Species = %q, want %q (owned by the adapter)", first.Species, "unused-dependency")
	}
	if first.File != "go.mod" {
		t.Errorf("File = %q, want go.mod", first.File)
	}
	// Script positions are already 1-based; passed through unchanged.
	wantSpan := engine.Span{StartLine: 7, StartCol: 2, EndLine: 7, EndCol: 20}
	if first.Span != wantSpan {
		t.Errorf("Span = %+v, want %+v", first.Span, wantSpan)
	}
	if first.Severity != engine.SeverityMedium {
		t.Errorf("Severity = %v, want medium", first.Severity)
	}
	if first.SourceLines != "\texample.com/unused v1.2.3" {
		t.Errorf("SourceLines = %q, want the verbatim indented source line", first.SourceLines)
	}
	if first.Meta["ruleId"] != "unused-dependency" {
		t.Errorf("Meta[ruleId] = %q, want unused-dependency", first.Meta["ruleId"])
	}

	// Second finding: defaults fill in optional fields.
	second := findings[1]
	wantSecond := engine.Span{StartLine: 8, StartCol: 1, EndLine: 8, EndCol: 1}
	if second.Span != wantSecond {
		t.Errorf("second Span = %+v, want %+v (defaults: endLine=line, col=1, endCol=col)", second.Span, wantSecond)
	}
	if second.Severity != engine.SeverityMedium {
		t.Errorf("second Severity = %v, want medium (default)", second.Severity)
	}
	if second.Meta["ruleId"] != "unused-dependency" {
		t.Errorf("second Meta[ruleId] = %q, want species-name fallback", second.Meta["ruleId"])
	}
}

// TestCommandEmptyOutputIsNoFindings: empty stdout means "no findings", not an error.
func TestCommandEmptyOutputIsNoFindings(t *testing.T) {
	for _, out := range []string{"", "   ", "[]"} {
		runner := func(_ context.Context, _ string, _ []string) ([]byte, error) { return []byte(out), nil }
		det := NewCommand("dead-config", "sh", "detect.sh", WithCommandRunner(runner))
		findings, err := det.Detect(context.Background(), engine.Scope{Root: "."})
		if err != nil {
			t.Fatalf("Detect(%q): unexpected error: %v", out, err)
		}
		if len(findings) != 0 {
			t.Errorf("Detect(%q): got %d findings, want 0", out, len(findings))
		}
	}
}

// TestCommandMissingInterpreterIsOperational: a missing interpreter is a typed
// DetectorUnavailableError (exit code 2), never a panic.
func TestCommandMissingInterpreterIsOperational(t *testing.T) {
	runner := func(_ context.Context, _ string, _ []string) ([]byte, error) {
		return nil, &exec.Error{Name: "no-such-interp", Err: exec.ErrNotFound}
	}
	det := NewCommand("unused-dependency", "no-such-interp", "detect.sh", WithCommandRunner(runner))
	_, err := det.Detect(context.Background(), engine.Scope{Root: "."})
	if err == nil {
		t.Fatal("Detect: want error for missing interpreter, got nil")
	}
	var unavail *engine.DetectorUnavailableError
	if !errors.As(err, &unavail) {
		t.Fatalf("error = %v, want *engine.DetectorUnavailableError", err)
	}
	if !errors.Is(err, engine.ErrOperational) {
		t.Error("missing interpreter must classify as engine.ErrOperational (exit code 2)")
	}
}

// TestCommandMalformedJSONFailsLoudly: a non-JSON payload is a wrapped error, not
// a silent zero-finding run (a misbehaving script must not look like "clean").
func TestCommandMalformedJSONFailsLoudly(t *testing.T) {
	runner := func(_ context.Context, _ string, _ []string) ([]byte, error) {
		return []byte("not json at all"), nil
	}
	det := NewCommand("unused-dependency", "sh", "detect.sh", WithCommandRunner(runner))
	if _, err := det.Detect(context.Background(), engine.Scope{Root: "."}); err == nil {
		t.Fatal("Detect: want error for malformed JSON, got nil")
	}
}

// TestCommandMissingRequiredFieldFailsLoudly: a finding missing file/line/message
// is rejected so a malformed script output never produces a half-built Finding.
func TestCommandMissingRequiredFieldFailsLoudly(t *testing.T) {
	cases := map[string]string{
		"no file":    `[{"line":1,"message":"m"}]`,
		"no line":    `[{"file":"go.mod","message":"m"}]`,
		"no message": `[{"file":"go.mod","line":1}]`,
		"bad sev":    `[{"file":"go.mod","line":1,"message":"m","severity":"catastrophic"}]`,
	}
	for name, payload := range cases {
		runner := func(_ context.Context, _ string, _ []string) ([]byte, error) { return []byte(payload), nil }
		det := NewCommand("unused-dependency", "sh", "detect.sh", WithCommandRunner(runner))
		if _, err := det.Detect(context.Background(), engine.Scope{Root: "."}); err == nil {
			t.Errorf("%s: want error, got nil", name)
		}
	}
}

// TestCommandTimeoutIsOperational: a runner that respects ctx and a fired deadline
// surfaces as an operational error (not a panic, not a silent empty result).
func TestCommandTimeoutIsOperational(t *testing.T) {
	runner := func(ctx context.Context, _ string, _ []string) ([]byte, error) {
		<-ctx.Done() // simulate a hung script that honours cancellation
		return nil, ctx.Err()
	}
	det := NewCommand("unused-dependency", "sh", "detect.sh",
		WithCommandRunner(runner), WithCommandTimeout(20*time.Millisecond))
	_, err := det.Detect(context.Background(), engine.Scope{Root: "."})
	if err == nil {
		t.Fatal("Detect: want timeout error, got nil")
	}
	if !errors.Is(err, engine.ErrOperational) {
		t.Errorf("timeout must classify as engine.ErrOperational, got %v", err)
	}
}
