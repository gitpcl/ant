package fix

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/gitpcl/ant/internal/engine"
)

// CommandRunner abstracts the exec call so the harness adapters' parse and
// timeout logic is testable against a RECORDED harness response — no live model
// binary (claude/codex/pi) is needed in CI (TECHSPEC §10, §12). It mirrors the
// seam detect/astgrep.go already uses for ast-grep and the injected *http.Client
// rawmodel uses for HTTP: production wires execRunner; tests (and embedders)
// stub it to return recorded stdout/exit/delay. It is exported so the
// black-box contract suite (package fix_test) can inject recorded responses.
//
// A runner is responsible for honouring ctx cancellation/deadline so a hung
// harness call surfaces as ctx.Err(); execRunner does this via
// exec.CommandContext. The deadline branch is what turns a hung harness into a
// skipped ant rather than a stalled colony (TECHSPEC §10 point 5).
type CommandRunner func(ctx context.Context, binary string, args []string, stdin string) (stdout []byte, err error)

// HarnessUnavailableError reports that a harness adapter's external binary could
// not be located or started — e.g. `pi`/`claude`/`codex` is not installed or not
// on PATH. Like the detector boundary, a harness is a plugin (TECHSPEC §2), so a
// missing binary is an operational condition, never a panic. It wraps
// engine.ErrOperational so errors.Is(err, engine.ErrOperational) classifies it,
// and — critically — it is still an error the colony loop turns into a SKIP
// (it does not special-case these adapters; a Fixer error is a skip).
type HarnessUnavailableError struct {
	Harness string // logical harness name, e.g. "pi"
	Binary  string // executable that could not be run
	Err     error  // underlying exec error
}

func (e *HarnessUnavailableError) Error() string {
	return fmt.Sprintf(
		"fix: harness %q unavailable: cannot run %q (is it installed and on PATH?): %v",
		e.Harness, e.Binary, e.Err,
	)
}

// Unwrap returns the underlying exec error.
func (e *HarnessUnavailableError) Unwrap() error { return e.Err }

// Is lets errors.Is(err, engine.ErrOperational) succeed so the exit-code
// classifier treats a missing harness binary as exit code 2 without importing
// the concrete type.
func (e *HarnessUnavailableError) Is(target error) bool {
	return target == engine.ErrOperational
}

// HarnessTimeoutError reports that a single harness invocation exceeded its
// deadline (the configured per-task Timeout, or a caller-supplied context
// deadline). This is the clean failure the §10 contract requires: the colony
// turns it into a skipped ant, never a stalled colony. It is deliberately NOT an
// operational error — a slow model on one finding should skip that finding, not
// abort the whole run with exit code 2.
type HarnessTimeoutError struct {
	Harness string        // logical harness name, e.g. "pi"
	Timeout time.Duration // the deadline that elapsed (zero if from caller ctx only)
	Err     error         // underlying context error (DeadlineExceeded/Canceled)
}

func (e *HarnessTimeoutError) Error() string {
	if e.Timeout > 0 {
		return fmt.Sprintf("fix: harness %q timed out after %s (hung call → skipped ant): %v",
			e.Harness, e.Timeout, e.Err)
	}
	return fmt.Sprintf("fix: harness %q call cancelled (→ skipped ant): %v", e.Harness, e.Err)
}

// Unwrap returns the underlying context error so errors.Is(err,
// context.DeadlineExceeded) / context.Canceled both succeed.
func (e *HarnessTimeoutError) Unwrap() error { return e.Err }

// harnessSpec is everything the shared driver needs to run one harness adapter:
// the logical name (for provenance + errors), the binary, the args + stdin built
// from the one FixTask, the per-task timeout, the injected runner, and a parser
// that turns the harness's structured stdout into a patch + rationale. Each
// adapter (pi/claudecode/codex) fills this in; the driver owns the exec, the
// deadline, and the failure-mode mapping ONCE (DRY — the rejected alternative
// was three copies of this).
type harnessSpec struct {
	name    string        // logical harness name, e.g. "pi" — used in errors + provenance
	model   string        // configured model id (TECHSPEC §2 — never hardcoded)
	binary  string        // executable name/path, resolved from PATH at run time
	args    []string      // args carrying exactly one localized FixTask
	stdin   string        // prompt fed on stdin (empty if passed via args)
	timeout time.Duration // bounds one call so a hung harness → skip
	runner  CommandRunner // injected; execRunner in production, stub in tests
	// parse maps the harness's structured stdout to a patch body. It returns the
	// raw model text (rationale) and the extracted unified diff. A parse failure
	// is a clean error (→ skip), never a panic.
	parse func(stdout []byte) (patch, rationale string, err error)
}

// runHarness executes one harness invocation under a bounded context, then maps
// the three failure modes the §10 contract calls out — missing binary, non-zero
// exit, and deadline exceeded — to clean typed errors, and on success parses the
// structured output into a ProposedDiff with mandatory provenance. It is the
// single place all three exec adapters share; statelessness falls out for free
// because runHarness keeps no state between calls (every call builds its own
// context and reads only its spec + task).
func runHarness(ctx context.Context, spec harnessSpec, task engine.FixTask) (engine.ProposedDiff, error) {
	// Bound this single call. A per-task deadline is what makes a hung harness a
	// skipped ant rather than a stalled colony (TECHSPEC §10 point 5). We honour
	// an already-shorter caller deadline by deriving from ctx.
	runCtx := ctx
	var cancel context.CancelFunc
	if spec.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, spec.timeout)
		defer cancel()
	}

	stdout, err := spec.runner(runCtx, spec.binary, spec.args, spec.stdin)
	if err != nil {
		// Order matters: a deadline that fired during exec can surface as a
		// generic run error, so check the context first.
		if cerr := runCtx.Err(); cerr != nil {
			return engine.ProposedDiff{}, &HarnessTimeoutError{Harness: spec.name, Timeout: spec.timeout, Err: cerr}
		}
		if isBinaryNotFound(err) {
			return engine.ProposedDiff{}, &HarnessUnavailableError{Harness: spec.name, Binary: spec.binary, Err: err}
		}
		return engine.ProposedDiff{}, fmt.Errorf("fix: harness %q run: %w", spec.name, err)
	}
	// A runner can return without error yet the deadline elapsed (e.g. a stub
	// that respects ctx but returns the partial output). Treat an elapsed
	// deadline as a timeout regardless of err so the skip path is airtight.
	if cerr := runCtx.Err(); cerr != nil {
		return engine.ProposedDiff{}, &HarnessTimeoutError{Harness: spec.name, Timeout: spec.timeout, Err: cerr}
	}

	patch, rationale, perr := spec.parse(stdout)
	if perr != nil {
		return engine.ProposedDiff{}, fmt.Errorf("fix: harness %q parse output: %w", spec.name, perr)
	}
	patch = extractPatch(patch)
	if strings.TrimSpace(patch) == "" {
		return engine.ProposedDiff{}, fmt.Errorf("fix: harness %q produced no usable diff", spec.name)
	}

	path := task.Context.File
	if path == "" {
		path = task.Finding.File
	}
	return engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: path, Patch: patch}},
		// Provenance is mandatory and reflects fixer + the CONFIG model
		// (TECHSPEC §10 point 3, §2). e.g. "pi (qwen2.5-coder)".
		Fixer:     fmt.Sprintf("%s (%s)", spec.name, spec.model),
		Rationale: rationale,
	}, nil
}

// execRunner is the production commandRunner: it runs the binary under the
// supplied context (so a deadline cancels the process), feeds stdin when given,
// and returns stdout. On a non-zero exit it surfaces stderr so the harness's own
// diagnostics reach the caller; a missing binary is returned unwrapped so
// runHarness can type it as HarnessUnavailableError.
func execRunner(ctx context.Context, binary string, args []string, stdin string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if isBinaryNotFound(err) {
			return nil, err // preserved so runHarness can type it
		}
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("%w: %s", err, bytes.TrimSpace(stderr.Bytes()))
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

// isBinaryNotFound reports whether err indicates the executable could not be
// located/started (the missing-binary case the §10 contract requires us to
// handle as a clean skip). exec returns *exec.Error (wrapping exec.ErrNotFound)
// when the binary is not on PATH. This mirrors detect/astgrep.go's helper; it is
// duplicated rather than shared across packages to keep detect and fix decoupled
// (each owns its own boundary; no cross-package coupling for a 6-line predicate).
func isBinaryNotFound(err error) bool {
	if err == nil {
		return false
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return true
	}
	return errors.Is(err, exec.ErrNotFound)
}
