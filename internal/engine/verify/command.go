package verify

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/gitpcl/ant/internal/engine"
)

// CommandCheckPrefix is the escape-hatch token prefix a species uses to declare
// a command verifier in [verify].checks, e.g. "command:verify.sh". The registry
// recognizes it by prefix (registry.go KnownVerifyKind); the colony and the
// fixture harness construct a commandVerifier from the suffix script path.
const CommandCheckPrefix = "command:"

// commandVerifyTimeout bounds a single command-verifier invocation so a hung
// gate script fails the check (a skip) rather than stalling the colony, mirroring
// the per-task deadline the harness adapters use (TECHSPEC §10).
const commandVerifyTimeout = 120 * time.Second

// ScriptRunner runs a verifier script in dir and returns combined output plus an
// error on a non-zero exit. It is injectable so the verifier's scratch-tree +
// result logic is testable WITHOUT a real interpreter or a real toolchain
// (TECHSPEC §12 — CI stays hermetic). Production uses scriptRunner (argv-form
// exec of interpreter + script).
type ScriptRunner func(ctx context.Context, dir string) (output []byte, err error)

// commandVerifier is the generic command: script-runner Verifier (the escape
// hatch the sprint depends on for install/parse/lint gates). It applies the
// proposed diff to a SCRATCH COPY of the tree (never the real working tree —
// scratch.go) and runs the species-declared verifier script THERE, so the gate
// observes the would-be post-fix state. Exit 0 = pass; a non-zero exit = fail
// with the script's combined stdout/stderr as the CheckResult detail, so the
// skip reason is the actual tool error (e.g. "go build" failing after a bad dep
// removal, or a YAML parser rejecting a malformed config).
//
// SECURITY (Sprint 020): like the command detector, this runs a species-supplied
// script — but always in argv form (interpreter + script path), NEVER via a
// shell, and confined to the scratch copy. The TRUST gate (an untrusted /
// freshly-installed community species' verifier must not run before review) is
// enforced UPSTREAM at the composition root (colony.verifierBuilder), which
// refuses to wire a commandVerifier for an unreviewed OriginUser species. This
// type is the mechanism, not the policy.
type commandVerifier struct {
	// name is the CheckResult name surfaced to review/--json, e.g.
	// "command:verify.sh" — the full token the manifest declared.
	name string
	// interp + script are the argv elements (no shell). script is resolved to the
	// species folder by the caller before construction.
	interp string
	script string
	// timeout bounds one invocation; zero uses commandVerifyTimeout.
	timeout time.Duration
	// run executes the script in the scratch root; injectable for hermetic tests.
	run ScriptRunner
}

// compile-time assertion that commandVerifier satisfies engine.Verifier.
var _ engine.Verifier = (*commandVerifier)(nil)

// CommandVerifierOption configures a commandVerifier.
type CommandVerifierOption func(*commandVerifier)

// WithScriptRunner injects a script runner for tests; production omits it and
// uses the real argv-form exec runner.
func WithScriptRunner(r ScriptRunner) CommandVerifierOption {
	return func(v *commandVerifier) {
		if r != nil {
			v.run = r
		}
	}
}

// WithCommandVerifyTimeout overrides the per-invocation deadline.
func WithCommandVerifyTimeout(t time.Duration) CommandVerifierOption {
	return func(v *commandVerifier) {
		if t > 0 {
			v.timeout = t
		}
	}
}

// NewCommandVerifier builds a command: script-runner verifier. name is the full
// check token (e.g. "command:verify.sh") surfaced as the CheckResult name; interp
// is the interpreter binary resolved from PATH at run time; script is the path it
// runs in the scratch root. A nil/unset runner uses the real exec runner.
func NewCommandVerifier(name, interp, script string, opts ...CommandVerifierOption) engine.Verifier {
	v := &commandVerifier{
		name:    name,
		interp:  interp,
		script:  script,
		timeout: commandVerifyTimeout,
		run:     scriptRunner(interp, script),
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// Verify copies the scope root, applies the diff, and runs the verifier script in
// the copy. A zero exit is a pass; a non-zero exit fails WITH the script output as
// the detail. A failure to build the scratch tree (copy/apply error) is itself a
// failed check — never a panic — so a malformed diff surfaces as a skip. A timeout
// fails the check with a clear reason.
func (v *commandVerifier) Verify(ctx context.Context, diff engine.ProposedDiff, scope engine.Scope) engine.VerifyResult {
	st, cleanup, err := newScratchTree(scope.Root, diff)
	if err != nil {
		return failResult(v.name, fmt.Sprintf("could not prepare scratch tree: %v", err))
	}
	defer cleanup()

	runCtx := ctx
	if v.timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, v.timeout)
		defer cancel()
	}

	out, err := v.run(runCtx, st.root)
	if cerr := runCtx.Err(); cerr != nil {
		return failResult(v.name, fmt.Sprintf("verifier script timed out: %v", cerr))
	}
	if err != nil {
		detail := fmt.Sprintf("verifier script failed: %v", err)
		if trimmed := bytes.TrimSpace(out); len(trimmed) > 0 {
			detail = fmt.Sprintf("verifier script failed: %s", trimmed)
		}
		return failResult(v.name, detail)
	}
	return passResult(v.name, "verifier script passed after the diff")
}

// scriptRunner is the production ScriptRunner: it execs interp + script in dir
// (argv form ONLY — no shell, so neither the script path nor dir can be
// interpreted as a shell command) under the supplied context, returning combined
// stdout+stderr so a tool error reaches the CheckResult detail verbatim.
func scriptRunner(interp, script string) ScriptRunner {
	return func(ctx context.Context, dir string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, interp, script)
		cmd.Dir = dir
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err := cmd.Run()
		return buf.Bytes(), err
	}
}

// ScriptFromCheck splits a "command:<script>" check token into the script suffix,
// returning ok=false for a token without the prefix or with an empty suffix. The
// colony and the fixture harness use it to turn a declared check into a script
// path (joined to the species folder) for NewCommandVerifier.
func ScriptFromCheck(check string) (script string, ok bool) {
	if !strings.HasPrefix(check, CommandCheckPrefix) {
		return "", false
	}
	suffix := strings.TrimPrefix(check, CommandCheckPrefix)
	if suffix == "" {
		return "", false
	}
	return suffix, true
}
