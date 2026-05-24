package verify

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"github.com/gitpcl/ant/internal/engine"
)

// CheckCompile is the canonical name of the compile check.
const CheckCompile = "compile"

// BuildCommand runs a project build/typecheck in dir and returns combined
// output plus an error on a non-zero exit. It is injectable so the verifier's
// scratch-tree + result logic is testable WITHOUT a real toolchain or the ant
// repo building itself (TECHSPEC §12 — CI needs no live build of a fixture it
// did not author). Production uses goBuildCommand (`go build ./...`).
type BuildCommand func(ctx context.Context, dir string) (output []byte, err error)

// compileVerifier checks that a proposed diff still builds. It applies the diff
// to a SCRATCH COPY of the tree (never the real working tree) and runs the build
// there, so a build-breaking fix is caught before it is ever staged
// (TECHSPEC §5.3). It does NOT lock: the colony serializes build-state verifiers
// behind the pool's per-project mutex (TECHSPEC §8.1), so adding a lock here
// would be redundant double-locking.
type compileVerifier struct {
	build BuildCommand
}

// compile-time assertion that compileVerifier satisfies engine.Verifier.
var _ engine.Verifier = (*compileVerifier)(nil)

// NewCompile returns a compile verifier. A nil build falls back to the real
// `go build ./...` runner; tests inject a fake to stay hermetic.
func NewCompile(build BuildCommand) engine.Verifier {
	if build == nil {
		build = goBuildCommand
	}
	return &compileVerifier{build: build}
}

// Verify copies the scope root, applies the diff, and runs the build in the
// copy. A clean build is a pass; a non-zero exit fails WITH the build output as
// the CheckResult detail, so the skip reason is the actual compiler error. A
// failure to build the scratch tree itself (copy/apply/spawn error) is also a
// failed check — never a panic — so a malformed diff surfaces as a skip.
func (v *compileVerifier) Verify(ctx context.Context, diff engine.ProposedDiff, scope engine.Scope) engine.VerifyResult {
	st, cleanup, err := newScratchTree(scope.Root, diff)
	if err != nil {
		return failResult(CheckCompile, fmt.Sprintf("could not prepare scratch tree: %v", err))
	}
	defer cleanup()

	out, err := v.build(ctx, st.root)
	if err != nil {
		detail := fmt.Sprintf("build failed: %v", err)
		if len(bytes.TrimSpace(out)) > 0 {
			detail = fmt.Sprintf("build failed: %s", bytes.TrimSpace(out))
		}
		return failResult(CheckCompile, detail)
	}
	return passResult(CheckCompile, "project builds cleanly after the diff")
}

// goBuildCommand is the production BuildCommand: `go build ./...` in dir,
// returning combined stdout+stderr so a build error reaches the CheckResult
// detail verbatim.
func goBuildCommand(ctx context.Context, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.Bytes(), err
}
