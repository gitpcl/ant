package fix

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gitpcl/ant/internal/engine"
)

// FixKindTool is the manifest [fix].kind token that selects the tool-runner
// (the orchestration species — formatter-drift, import-sort, lint-autofix,
// trailing-debug-code — declare it). It is the engine-side spelling the species
// registry validates and colony.buildFixer dispatches on. Kept here next to the
// adapter so the one place that knows the token is the one that implements it.
const FixKindTool = "tool"

// PlaceholderFile is the token a manifest's [fix].args list may contain; the
// tool-runner substitutes it with the scratch copy's path before exec. It makes
// the wrapped command fully DECLARATIVE: gofmt wants the path as a positional
// arg (`gofmt -w {file}`), prettier wants `--write {file}`, ruff wants
// `--fix {file}` — the species author places {file} where the tool expects it
// rather than the engine special-casing each tool (sprint contract: "no per-tool
// special-casing — the command is declarative").
const PlaceholderFile = "{file}"

// defaultToolTimeout bounds one tool invocation when the manifest declares none.
// A formatter that hangs becomes a clean per-ant skip (the §10 contract), never a
// stalled colony, so an unbounded run is never allowed.
const defaultToolTimeout = 30 * time.Second

// ToolConfig is the manifest-declared external command the tool-runner wraps
// (TECHSPEC §10, sprint contract). It is the typed view of the species.toml
// [fix] section for kind="tool":
//
//	[fix]
//	kind    = "tool"
//	command = "gofmt"
//	args    = ["-w", "{file}"]
//	timeout = "30s"          # optional; defaults to defaultToolTimeout
//
// Command is the executable resolved from PATH at Fix time (a missing binary is a
// clean skip, like the harness adapters). Args are passed verbatim with the
// {file} placeholder substituted for the scratch copy's path; an Args list with
// no placeholder appends the scratch path as the final argument so the common
// "tool [flags] <file>" shape needs no placeholder. Version, when set, names the
// command that reports the tool version for provenance enrichment (best-effort).
type ToolConfig struct {
	Command string
	Args    []string
	Timeout time.Duration
	// VersionArgs, when non-empty, is run once (e.g. ["--version"]) to capture the
	// tool's version string for provenance. A failure to read the version is NOT
	// fatal — provenance falls back to the bare command name. It is declarative
	// like Args so no tool is special-cased.
	VersionArgs []string
	// runner is the injected exec seam (execRunner in production, a stub in tests).
	// It mirrors the harness adapters' CommandRunner so the tool-runner reuses the
	// SAME §10 exec/timeout machinery rather than re-rolling process exec.
	runner CommandRunner
}

// toolFixer runs a manifest-declared external formatter/autofixer on a SCRATCH
// COPY of one target file and captures the resulting whole-file change as a
// ProposedDiff (TECHSPEC §5.2, §10). It wraps existing ecosystems (gofmt,
// prettier, ruff, eslint, clippy) under Ant's staging/review/trust model: the
// engine only execs and diffs — what to run is declarative in species.toml.
//
// It is stateless between tasks (every Fix builds its own scratch dir and reads
// only its config + the one task) and bounds each invocation so a hung tool is a
// skipped ant, not a stalled colony — reusing the runHarness exec/timeout
// contract via the shared CommandRunner seam (exec.go).
type toolFixer struct {
	cfg ToolConfig
}

// compile-time assertion that toolFixer satisfies engine.Fixer.
var _ engine.Fixer = (*toolFixer)(nil)

// NewTool returns a tool-runner Fixer for the manifest-declared command. An empty
// command is rejected up front (a misconfigured species fails loudly at build
// time, like the rawmodel adapter's missing-model check). The binary itself is
// not probed until Fix runs, so a missing tool surfaces as a clean per-ant skip,
// never a constructor panic.
func NewTool(cfg ToolConfig) (engine.Fixer, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, fmt.Errorf("fix: tool-runner requires a [fix].command in the species manifest")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultToolTimeout
	}
	if cfg.runner == nil {
		cfg.runner = execRunner
	}
	return &toolFixer{cfg: cfg}, nil
}

// NewToolWithRunner is NewTool with an injected command runner for tests that use
// a FAKE tool binary / recorded behavior instead of a real formatter on PATH
// (TECHSPEC §10, §12 — CI must not depend on gofmt/prettier/ruff/eslint).
func NewToolWithRunner(cfg ToolConfig, runner CommandRunner) (engine.Fixer, error) {
	cfg.runner = runner
	return NewTool(cfg)
}

// Fix copies the target file into a scratch dir, runs the declared command over
// the copy under a bounded context, reads the post-command content, and emits a
// whole-file unified diff (original → formatted) with tool provenance. The four
// §10 failure modes map to clean per-ant skips:
//   - missing binary  → HarnessUnavailableError (operational, still a skip)
//   - non-zero exit    → wrapped run error (skip)
//   - timeout          → HarnessTimeoutError (skip, never a stalled colony)
//   - no change        → a clear "tool made no changes" error (skip; nothing to stage)
//
// The real working tree is only READ (the target file is copied out); every write
// lands under the temp dir, so the fixer honors the never-touch-the-tree
// guarantee the staging model depends on.
func (f *toolFixer) Fix(ctx context.Context, task engine.FixTask) (engine.ProposedDiff, error) {
	path, err := taskPath(task)
	if err != nil {
		return engine.ProposedDiff{}, err
	}

	original, err := os.ReadFile(path)
	if err != nil {
		// A target the tool-runner cannot read is an operational condition (the
		// detector named a file that is gone/unreadable); surface it as a skip.
		return engine.ProposedDiff{}, fmt.Errorf("%w: fix: tool-runner cannot read target %s: %v", engine.ErrOperational, path, err)
	}

	formatted, err := f.runTool(ctx, path, original)
	if err != nil {
		return engine.ProposedDiff{}, err
	}

	if string(formatted) == string(original) {
		// The tool ran cleanly but changed nothing — there is no drift to fix.
		// That is not a panic and not a silent drop: it is a clean skip the colony
		// surfaces, exactly like a verifier that finds nothing to gate.
		return engine.ProposedDiff{}, fmt.Errorf("fix: tool %q made no changes to %s (no drift to fix)", f.cfg.Command, path)
	}

	patch := wholeFileDiff(path, string(original), string(formatted))
	return engine.ProposedDiff{
		Files:     []engine.FileDiff{{Path: path, Patch: patch}},
		Fixer:     f.provenance(ctx),
		Rationale: fmt.Sprintf("ran %q over %s and captured the reformatted output (no model involved)", f.commandLine(), path),
	}, nil
}

// runTool writes original into a scratch copy, execs the declared command over
// it under a bounded context (the shared §10 contract), and returns the
// post-command file content. The three exec failure modes are typed exactly as
// the harness driver types them so the colony's skip path is shared:
// missing-binary → HarnessUnavailableError, timeout → HarnessTimeoutError,
// non-zero exit → a wrapped run error.
func (f *toolFixer) runTool(ctx context.Context, path string, original []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "ant-tool-")
	if err != nil {
		return nil, fmt.Errorf("%w: fix: tool-runner scratch dir: %v", engine.ErrOperational, err)
	}
	defer os.RemoveAll(dir)

	// Preserve the file's base name so a tool that dispatches on extension
	// (prettier, eslint) sees the right language; the directory is throwaway.
	scratchFile := filepath.Join(dir, filepath.Base(path))
	if err := os.WriteFile(scratchFile, original, 0o600); err != nil {
		return nil, fmt.Errorf("%w: fix: tool-runner write scratch copy: %v", engine.ErrOperational, err)
	}

	args := substituteArgs(f.cfg.Args, scratchFile)

	// Bound this single invocation — a hung tool becomes a skipped ant, not a
	// stalled colony (TECHSPEC §10 point 5). Honour an already-shorter caller
	// deadline by deriving from ctx.
	runCtx := ctx
	if f.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, f.cfg.Timeout)
		defer cancel()
	}

	_, runErr := f.cfg.runner(runCtx, f.cfg.Command, args, "")
	if runErr != nil {
		// Order matters: a deadline that fired during exec can surface as a generic
		// run error, so check the context first (mirrors runHarness).
		if cerr := runCtx.Err(); cerr != nil {
			return nil, &HarnessTimeoutError{Harness: "tool:" + f.cfg.Command, Timeout: f.cfg.Timeout, Err: cerr}
		}
		if isBinaryNotFound(runErr) {
			return nil, &HarnessUnavailableError{Harness: "tool:" + f.cfg.Command, Binary: f.cfg.Command, Err: runErr}
		}
		return nil, fmt.Errorf("fix: tool %q exited non-zero over %s: %w", f.cfg.Command, path, runErr)
	}
	// A runner can return without error yet the deadline elapsed; treat an elapsed
	// deadline as a timeout regardless so the skip path is airtight.
	if cerr := runCtx.Err(); cerr != nil {
		return nil, &HarnessTimeoutError{Harness: "tool:" + f.cfg.Command, Timeout: f.cfg.Timeout, Err: cerr}
	}

	// The tool edits the file in place (the declared args use -w/--write/--fix
	// over {file}); read the post-command content back from the scratch copy.
	formatted, err := os.ReadFile(scratchFile)
	if err != nil {
		return nil, fmt.Errorf("%w: fix: tool-runner read formatted scratch copy: %v", engine.ErrOperational, err)
	}
	return formatted, nil
}

// provenance builds the Fixer string: "tool (<command> <version>)" when a version
// is readable, else "tool (<command>)". The version probe is best-effort and
// bounded by the same timeout; a failure never blocks the fix (TECHSPEC §10 point
// 3 — provenance is mandatory, but a missing version is not fatal).
func (f *toolFixer) provenance(ctx context.Context) string {
	if v := f.toolVersion(ctx); v != "" {
		return fmt.Sprintf("tool (%s %s)", f.cfg.Command, v)
	}
	return fmt.Sprintf("tool (%s)", f.cfg.Command)
}

// toolVersion runs the declared VersionArgs once and returns the first line of
// output as the version string. Any failure (no VersionArgs, missing binary,
// timeout, empty output) yields "" so provenance falls back to the bare command.
func (f *toolFixer) toolVersion(ctx context.Context) string {
	if len(f.cfg.VersionArgs) == 0 {
		return ""
	}
	runCtx := ctx
	if f.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, f.cfg.Timeout)
		defer cancel()
	}
	out, err := f.cfg.runner(runCtx, f.cfg.Command, f.cfg.VersionArgs, "")
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	return sanitizeVersion(strings.TrimSpace(line))
}

// maxVersionLen bounds the version substring folded into provenance so a tool
// that emits a pathologically long --version line cannot bloat the Fixer string.
const maxVersionLen = 64

// sanitizeVersion strips control bytes from an external tool's --version output
// before it becomes part of the provenance Fixer string. The tool is untrusted
// input (TECHSPEC §2 — a plugin), so a malicious/garbled tool could emit ANSI
// escape sequences or a carriage return to spoof terminal output when provenance
// is rendered in the TUI/`ant review` (the --json stream is already safe — JSON
// encoding neutralizes control bytes). It drops every byte < 0x20 (control chars,
// including CR/LF/ESC) and 0x7f (DEL), keeping ordinary printable bytes (normal
// spaces included), then bounds the length. The result is a plain, render-safe
// token; an empty result (a version line that was entirely control bytes) falls
// through to the bare-command provenance.
func sanitizeVersion(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 0x20 && c != 0x7f {
			b.WriteByte(c)
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > maxVersionLen {
		out = strings.TrimSpace(out[:maxVersionLen])
	}
	return out
}

// commandLine renders the declared command + args for the rationale, leaving the
// {file} placeholder visible so the explanation reads as the species author wrote
// it rather than a temp path.
func (f *toolFixer) commandLine() string {
	if len(f.cfg.Args) == 0 {
		return f.cfg.Command
	}
	return f.cfg.Command + " " + strings.Join(f.cfg.Args, " ")
}

// substituteArgs replaces every {file} placeholder in args with scratchFile. When
// args contains no placeholder the scratch path is appended as the final argument,
// so the common "tool [flags] <file>" shape works without an explicit placeholder.
// A copy is returned so the manifest's Args slice is never mutated (immutability —
// the next task reuses the same config).
func substituteArgs(args []string, scratchFile string) []string {
	out := make([]string, 0, len(args)+1)
	replaced := false
	for _, a := range args {
		if a == PlaceholderFile {
			out = append(out, scratchFile)
			replaced = true
			continue
		}
		if strings.Contains(a, PlaceholderFile) {
			out = append(out, strings.ReplaceAll(a, PlaceholderFile, scratchFile))
			replaced = true
			continue
		}
		out = append(out, a)
	}
	if !replaced {
		out = append(out, scratchFile)
	}
	return out
}

// wholeFileDiff renders a unified-diff patch that replaces the entire original
// content with the formatted content. It emits a single hunk that removes every
// original line and adds every formatted line, in the constrained dialect
// verify/scratch.go's applyUnifiedPatch consumes (`@@ -1,N +1,M @@` then `-`/`+`
// lines). Because every `-` line is the verbatim original line in order, the
// scratch-tree apply's exact-match check passes; the resulting file is exactly
// the formatted content.
//
// A whole-file replacement (rather than a minimal line-level diff) is the right
// model for a formatter: the tool's output IS the new file, and emitting it
// verbatim keeps the fixer free of a diff-minimization algorithm the verifier
// gate (compile, idempotence) does not need. diff-bounded still caps total
// changed lines, so a runaway whole-file rewrite is rejected by the gate.
func wholeFileDiff(path, original, formatted string) string {
	oldLines := diffLines(original)
	newLines := diffLines(formatted)

	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n", path)
	fmt.Fprintf(&b, "+++ b/%s\n", path)
	fmt.Fprintf(&b, "@@ -1,%d +1,%d @@\n", len(oldLines), len(newLines))
	for _, ln := range oldLines {
		fmt.Fprintf(&b, "-%s\n", ln)
	}
	for _, ln := range newLines {
		fmt.Fprintf(&b, "+%s\n", ln)
	}
	return b.String()
}

// diffLines splits content into lines for the whole-file diff, normalizing CRLF
// and dropping a single trailing empty element from a terminating newline so the
// `-`/`+` line counts match verify/scratch.go's splitKeepLines (the two MUST
// agree or the apply's context check would misalign).
func diffLines(content string) []string {
	if content == "" {
		return nil
	}
	s := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}
