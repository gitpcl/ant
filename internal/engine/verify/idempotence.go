package verify

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gitpcl/ant/internal/engine"
)

// CheckFormatterIdempotence is the canonical name of the idempotence check and
// the manifest [verify] token the orchestration species declare. Kept here next
// to the verifier so the one place that knows the token is the one that
// implements it (mirrors CheckCompile / CheckDetectorClears).
const CheckFormatterIdempotence = "formatter-idempotence"

// PlaceholderFile is the token an idempotence verifier's args list may contain;
// it is substituted with the post-fix file path before the second run. It
// mirrors fix.PlaceholderFile so a species author writes the SAME {file}
// placeholder for the tool-runner fix and its idempotence verifier. It is
// duplicated (not imported from fix) so verify stays decoupled from fix — each
// owns its boundary, like detect and fix each own their own isBinaryNotFound.
const PlaceholderFile = "{file}"

// ToolRunner abstracts the exec call the idempotence verifier makes so the
// re-run is testable against a FAKE tool binary / recorded behavior — no real
// gofmt/prettier/ruff/eslint is needed in CI (sprint contract, TECHSPEC §12). It
// is the same shape as fix.CommandRunner but declared here to keep verify
// decoupled from the fix package. Production wires execToolRunner; tests inject a
// stub that mutates (or leaves stable) the scratch file to model an oscillating
// (or idempotent) formatter.
type ToolRunner func(ctx context.Context, command string, args []string, dir string) error

// ToolSpec is the manifest-declared command the idempotence verifier re-runs. It
// mirrors the [fix] tool section so a species declares the formatter once and the
// verifier re-runs the SAME command:
//
//	[verify]
//	checks = ["formatter-idempotence", "compile"]
//	[verify.tool]
//	command = "gofmt"
//	args    = ["-w", "{file}"]
//
// Command is resolved from PATH at Verify time; Args are passed verbatim with the
// {file} placeholder substituted for the post-fix file path (an Args list with no
// placeholder appends the file as the final argument). A missing binary or a
// non-zero exit is a FAILED check (the formatter could not confirm convergence),
// never a panic — surfaced as a skip with detail, like every other verifier.
type ToolSpec struct {
	Command string
	Args    []string
}

// idempotenceVerifier re-runs a formatter over the POST-FIX tree and passes only
// when the second run yields NO further changes (the sprint contract's trust
// signal: a stable formatter converges; an oscillating/non-idempotent one never
// does and MUST fail rather than loop). It applies the fix's diff to a SCRATCH
// COPY (never the real tree), runs the tool over the patched file there, and
// compares the file before and after the second run.
//
// It guards a formatter that never converges: rather than the colony re-running
// fix→verify forever, a non-idempotent tool fails idempotence ONCE with detail,
// and the diff is skipped (PRD §6.3 — a skip is a trust signal, not a hidden
// error). This is why idempotence is a verifier, not a retry loop.
type idempotenceVerifier struct {
	spec   ToolSpec
	runner ToolRunner
}

// compile-time assertion that idempotenceVerifier satisfies engine.Verifier.
var _ engine.Verifier = (*idempotenceVerifier)(nil)

// NewFormatterIdempotence returns an idempotence verifier for the manifest's
// declared tool. A nil runner falls back to the real exec runner; tests inject a
// fake so CI needs no installed formatter.
func NewFormatterIdempotence(spec ToolSpec, runner ToolRunner) engine.Verifier {
	if runner == nil {
		runner = execToolRunner
	}
	return &idempotenceVerifier{spec: spec, runner: runner}
}

// Verify applies the diff to a scratch copy, re-runs the tool over the patched
// file, and passes only when the file is byte-identical before and after the
// second run. The check fails (with detail) when the tool oscillates (produces
// further changes) or cannot be run, so a formatter that never converges is a
// surfaced skip, never an infinite loop. A scratch-prep/IO failure is a failed
// check too — never a panic.
func (v *idempotenceVerifier) Verify(ctx context.Context, diff engine.ProposedDiff, scope engine.Scope) engine.VerifyResult {
	if strings.TrimSpace(v.spec.Command) == "" {
		return failResult(CheckFormatterIdempotence, "no tool command declared for the idempotence check (set [verify.tool].command in the species manifest)")
	}

	st, cleanup, err := newScratchTree(scope.Root, diff)
	if err != nil {
		return failResult(CheckFormatterIdempotence, fmt.Sprintf("could not prepare scratch tree: %v", err))
	}
	defer cleanup()

	// Idempotence is a per-file property; check each file the diff touched and fail
	// on the FIRST that the tool would keep changing.
	//
	// We run the tool TWICE and compare the two TOOL OUTPUTS, not the diff-applied
	// content against the first output. The diff the fixer staged is a faithful
	// reconstruction of the tool's result, but the scratch-tree apply can differ
	// from the tool's on-disk write in pure trivia (a trailing newline the
	// unified-diff form drops), which is NOT formatter drift. Comparing run-1 vs
	// run-2 measures the real property — "the tool on its own output makes no
	// change" — immune to that reconstruction artifact: run 1 canonicalizes to the
	// tool's own output, run 2 must leave it byte-identical.
	for _, fd := range diff.Files {
		target := filepath.Join(st.root, filepath.FromSlash(fd.Path))
		args := substituteToolArgs(v.spec.Args, target)

		if runErr := v.runner(ctx, v.spec.Command, args, st.root); runErr != nil {
			return failResult(CheckFormatterIdempotence, fmt.Sprintf("re-running %q over %s failed: %v", v.spec.Command, fd.Path, runErr))
		}
		firstOut, rerr := os.ReadFile(target)
		if rerr != nil {
			return failResult(CheckFormatterIdempotence, fmt.Sprintf("cannot read %s after the first re-run: %v", fd.Path, rerr))
		}

		if runErr := v.runner(ctx, v.spec.Command, args, st.root); runErr != nil {
			return failResult(CheckFormatterIdempotence, fmt.Sprintf("re-running %q over %s failed: %v", v.spec.Command, fd.Path, runErr))
		}
		secondOut, rerr := os.ReadFile(target)
		if rerr != nil {
			return failResult(CheckFormatterIdempotence, fmt.Sprintf("cannot read %s after the second re-run: %v", fd.Path, rerr))
		}

		if !bytes.Equal(firstOut, secondOut) {
			return failResult(CheckFormatterIdempotence, fmt.Sprintf(
				"tool %q is NOT idempotent on %s: a second run produced further changes (the formatter does not converge — refusing to trust an oscillating tool)",
				v.spec.Command, fd.Path))
		}
	}

	return passResult(CheckFormatterIdempotence, fmt.Sprintf(
		"tool %q re-run produced no further changes (the diff is stable/converged)", v.spec.Command))
}

// execToolRunner is the production ToolRunner: it runs the in-place formatter
// under the supplied context (so a deadline cancels the process) with the working
// directory set to the scratch root, and returns a non-nil error on a non-zero
// exit (which the verifier turns into a failed check). It surfaces stderr so the
// tool's own diagnostics reach the CheckResult detail.
func execToolRunner(ctx context.Context, command string, args []string, dir string) error {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("%w: %s", err, bytes.TrimSpace(stderr.Bytes()))
		}
		return err
	}
	return nil
}

// substituteToolArgs replaces every {file} placeholder in args with target, or
// appends target when no placeholder is present (the common "tool [flags] <file>"
// shape). A copy is returned so the spec's Args slice is never mutated.
func substituteToolArgs(args []string, target string) []string {
	out := make([]string, 0, len(args)+1)
	replaced := false
	for _, a := range args {
		if a == PlaceholderFile {
			out = append(out, target)
			replaced = true
			continue
		}
		if strings.Contains(a, PlaceholderFile) {
			out = append(out, strings.ReplaceAll(a, PlaceholderFile, target))
			replaced = true
			continue
		}
		out = append(out, a)
	}
	if !replaced {
		out = append(out, target)
	}
	return out
}
