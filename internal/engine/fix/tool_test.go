package fix

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/verify"
)

// writeTarget writes content to a temp dir, chdirs into it (so the tool-runner
// reads the target by its ROOT-RELATIVE path, exactly as the colony passes a
// finding's File relative to scope.Root), and returns the relative name. The
// scratch-tree verifiers resolve diff paths relative to scope.Root too, so the
// relative shape is what the whole pipeline expects.
func writeTarget(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	t.Chdir(dir)
	return name
}

// fakeFormatterRunner returns a CommandRunner that simulates an in-place
// formatter: it locates the scratch-file argument (the last arg, or the one the
// {file} placeholder resolved to) and rewrites it via transform. It models the
// exec contract (it writes the file, like gofmt -w) WITHOUT any real binary, so
// the test proves the tool-runner's read→exec→diff loop deterministically.
func fakeFormatterRunner(transform func(string) string) CommandRunner {
	return func(_ context.Context, _ string, args []string, _ string) ([]byte, error) {
		if len(args) == 0 {
			return nil, errors.New("fake formatter: no file argument")
		}
		file := args[len(args)-1]
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		if werr := os.WriteFile(file, []byte(transform(string(b))), 0o600); werr != nil {
			return nil, werr
		}
		return nil, nil
	}
}

// TestTool_CapturesDiffWithProvenance is the core feature-1 assertion: the
// tool-runner runs a (fake) external formatter over a scratch copy and captures
// the resulting change as a ProposedDiff with tool provenance — no real formatter
// on PATH (TECHSPEC §10, §12).
func TestTool_CapturesDiffWithProvenance(t *testing.T) {
	// Drifted Go source: a line with trailing whitespace a formatter would strip.
	target := writeTarget(t, "drift.go", "package p\n\nvar X = 1  \n")
	runner := fakeFormatterRunner(func(s string) string {
		// Strip trailing spaces on each line (a stand-in for gofmt).
		lines := strings.Split(s, "\n")
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " ")
		}
		return strings.Join(lines, "\n")
	})

	fixer, err := NewToolWithRunner(ToolConfig{Command: "fakefmt", Args: []string{"-w", PlaceholderFile}}, runner)
	if err != nil {
		t.Fatalf("NewToolWithRunner: %v", err)
	}

	diff, err := fixer.Fix(context.Background(), engine.FixTask{
		Finding: engine.Finding{File: target},
		Context: engine.CodeContext{File: target},
	})
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if len(diff.Files) != 1 {
		t.Fatalf("Fix produced %d file diffs, want 1", len(diff.Files))
	}
	if !strings.HasPrefix(diff.Fixer, "tool (fakefmt") {
		t.Errorf("provenance = %q, want it to start with %q", diff.Fixer, "tool (fakefmt")
	}

	// The captured diff must apply to the original tree to yield the formatted
	// content — prove it through the SAME apply machinery the verifiers use, so the
	// whole-file diff dialect is genuinely consumable downstream.
	scope := engine.Scope{Root: "."}
	res := verify.NewCompileFor(func(context.Context, string) ([]byte, error) { return nil, nil }).
		Verify(context.Background(), diff, scope)
	if !res.Passed {
		t.Fatalf("the tool diff did not apply cleanly to a scratch tree: %v", res.Checks)
	}
}

// TestTool_VersionInProvenance asserts a readable tool version is folded into
// provenance ("tool (<cmd> <version>)") when VersionArgs is declared.
func TestTool_VersionInProvenance(t *testing.T) {
	target := writeTarget(t, "drift.go", "package p\nvar X = 1  \n")
	runner := func(_ context.Context, _ string, args []string, _ string) ([]byte, error) {
		// The version probe is the only call with the version flag.
		if len(args) == 1 && args[0] == "--version" {
			return []byte("fakefmt v9.9.9\n"), nil
		}
		file := args[len(args)-1]
		b, _ := os.ReadFile(file)
		_ = os.WriteFile(file, []byte(strings.ReplaceAll(string(b), "  ", "")), 0o600)
		return nil, nil
	}
	fixer, err := NewToolWithRunner(ToolConfig{
		Command:     "fakefmt",
		Args:        []string{"-w", PlaceholderFile},
		VersionArgs: []string{"--version"},
	}, runner)
	if err != nil {
		t.Fatalf("NewToolWithRunner: %v", err)
	}
	diff, err := fixer.Fix(context.Background(), engine.FixTask{Context: engine.CodeContext{File: target}})
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if diff.Fixer != "tool (fakefmt fakefmt v9.9.9)" {
		t.Errorf("provenance = %q, want %q", diff.Fixer, "tool (fakefmt fakefmt v9.9.9)")
	}
}

// TestTool_VersionSanitizedInProvenance closes LOW-1 (security): an untrusted
// tool's --version output is render-unsafe input. A version line carrying a
// carriage return, an ANSI escape sequence, and control bytes must NOT leak into
// the provenance Fixer string (which is rendered in the TUI / `ant review`); the
// control bytes are stripped, leaving only printable characters.
func TestTool_VersionSanitizedInProvenance(t *testing.T) {
	target := writeTarget(t, "drift.go", "package p\nvar X = 1  \n")
	// A malicious/garbled --version: ANSI red + CR carriage-return spoof + a NUL +
	// the real-looking token, then a second physical line that must be dropped.
	malicious := "\x1b[31mevil\rfmt\x00 v1.2.3\nSECOND LINE SHOULD BE DROPPED\n"
	runner := func(_ context.Context, _ string, args []string, _ string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte(malicious), nil
		}
		file := args[len(args)-1]
		b, _ := os.ReadFile(file)
		_ = os.WriteFile(file, []byte(strings.ReplaceAll(string(b), "  ", "")), 0o600)
		return nil, nil
	}
	fixer, err := NewToolWithRunner(ToolConfig{
		Command:     "fakefmt",
		Args:        []string{"-w", PlaceholderFile},
		VersionArgs: []string{"--version"},
	}, runner)
	if err != nil {
		t.Fatalf("NewToolWithRunner: %v", err)
	}
	diff, err := fixer.Fix(context.Background(), engine.FixTask{Context: engine.CodeContext{File: target}})
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}

	// No control byte (< 0x20) and no DEL (0x7f) may survive into provenance.
	for i := 0; i < len(diff.Fixer); i++ {
		if c := diff.Fixer[i]; c < 0x20 || c == 0x7f {
			t.Fatalf("provenance %q contains control byte 0x%02x at index %d (LOW-1 not closed)", diff.Fixer, c, i)
		}
	}
	// Specifically: CR, ESC, and the second line's text are gone; the printable
	// token survives. The ESC byte is stripped but its trailing "[31m" letters are
	// printable and remain — that is acceptable (they are inert text, not an active
	// escape sequence without the ESC introducer).
	if strings.ContainsAny(diff.Fixer, "\r\x1b\x00") {
		t.Errorf("provenance %q still contains CR/ESC/NUL", diff.Fixer)
	}
	if strings.Contains(diff.Fixer, "SECOND LINE") {
		t.Errorf("provenance %q leaked a second --version line", diff.Fixer)
	}
	if !strings.Contains(diff.Fixer, "v1.2.3") {
		t.Errorf("provenance %q dropped the printable version token", diff.Fixer)
	}
}

// TestSanitizeVersion covers the control-byte filter and length bound directly.
func TestSanitizeVersion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "gofmt 1.21", "gofmt 1.21"},
		{"strips CR", "good\rbad", "goodbad"},
		{"strips ESC/ANSI introducer", "\x1b[1mver\x1b[0m", "[1mver[0m"},
		{"strips NUL and DEL", "a\x00b\x7fc", "abc"},
		{"keeps normal spaces", "tool  v1", "tool  v1"},
		{"all control collapses to empty", "\r\n\x00\x1b", ""},
		{"bounded length", strings.Repeat("x", 200), strings.Repeat("x", maxVersionLen)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeVersion(c.in); got != c.want {
				t.Errorf("sanitizeVersion(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestTool_NoChangeIsCleanSkip asserts a tool that runs cleanly but changes
// nothing yields a clean error (→ colony skip), never an empty diff.
func TestTool_NoChangeIsCleanSkip(t *testing.T) {
	target := writeTarget(t, "clean.go", "package p\n")
	runner := fakeFormatterRunner(func(s string) string { return s }) // no-op formatter
	fixer, _ := NewToolWithRunner(ToolConfig{Command: "fakefmt", Args: []string{PlaceholderFile}}, runner)

	_, err := fixer.Fix(context.Background(), engine.FixTask{Context: engine.CodeContext{File: target}})
	if err == nil {
		t.Fatal("Fix on already-formatted file returned nil error, want a clean no-change skip")
	}
	if !strings.Contains(err.Error(), "no changes") {
		t.Errorf("error = %v, want it to mention no changes", err)
	}
}

// TestTool_TimeoutIsCleanSkip is the §10 assertion: a hung tool becomes a clean
// per-ant skip (HarnessTimeoutError → context.DeadlineExceeded), never a panic or
// a stalled colony.
func TestTool_TimeoutIsCleanSkip(t *testing.T) {
	target := writeTarget(t, "slow.go", "package p\nvar X = 1  \n")
	hung := func(ctx context.Context, _ string, _ []string, _ string) ([]byte, error) {
		<-ctx.Done() // model a hung tool: block until the deadline fires
		return nil, ctx.Err()
	}
	fixer, _ := NewToolWithRunner(ToolConfig{
		Command: "hangfmt",
		Args:    []string{PlaceholderFile},
		Timeout: 20 * time.Millisecond,
	}, hung)

	_, err := fixer.Fix(context.Background(), engine.FixTask{Context: engine.CodeContext{File: target}})
	if err == nil {
		t.Fatal("Fix with a hung tool returned nil, want a clean timeout skip")
	}
	var te *HarnessTimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("error = %v (%T), want a *HarnessTimeoutError", err, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("timeout error does not unwrap to context.DeadlineExceeded: %v", err)
	}
}

// TestTool_MissingBinaryIsOperationalSkip asserts a tool not on PATH surfaces as
// a HarnessUnavailableError (operational, still a colony skip), using the REAL
// execRunner against a name that cannot resolve — no mock, proving the production
// exec path types a missing binary correctly.
func TestTool_MissingBinaryIsOperationalSkip(t *testing.T) {
	target := writeTarget(t, "x.go", "package p\nvar X = 1  \n")
	fixer, err := NewTool(ToolConfig{Command: "ant-no-such-formatter-xyz", Args: []string{PlaceholderFile}})
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	_, ferr := fixer.Fix(context.Background(), engine.FixTask{Context: engine.CodeContext{File: target}})
	if ferr == nil {
		t.Fatal("Fix with a missing binary returned nil, want a clean skip")
	}
	if !errors.Is(ferr, engine.ErrOperational) {
		t.Errorf("missing-binary error = %v, want errors.Is(ErrOperational)", ferr)
	}
}

// TestTool_EmptyCommandRejected asserts a misconfigured species (no command)
// fails loudly at construction, not at fix time.
func TestTool_EmptyCommandRejected(t *testing.T) {
	if _, err := NewTool(ToolConfig{Command: "   "}); err == nil {
		t.Fatal("NewTool with a blank command returned nil error, want a loud rejection")
	}
}

// TestSubstituteArgs covers placeholder substitution and the append fallback.
func TestSubstituteArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"placeholder", []string{"-w", PlaceholderFile}, []string{"-w", "/scratch/f.go"}},
		{"no placeholder appends", []string{"--fix"}, []string{"--fix", "/scratch/f.go"}},
		{"embedded placeholder", []string{"--path=" + PlaceholderFile}, []string{"--path=/scratch/f.go"}},
		{"empty appends", nil, []string{"/scratch/f.go"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := substituteArgs(c.args, "/scratch/f.go")
			if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
				t.Errorf("substituteArgs(%v) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}
