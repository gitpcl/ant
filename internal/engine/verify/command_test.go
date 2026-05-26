package verify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitpcl/ant/internal/engine"
)

// scratchRepo writes a minimal tree so the command verifier has a real scope root
// to copy into a scratch dir (the verifier applies the diff to the copy, never the
// original). Returns the root path.
func scratchRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	return root
}

// TestCommandVerifierPassesOnZeroExit: a script that exits 0 passes the gate, and
// it runs in the SCRATCH copy (its working dir is not the original root).
func TestCommandVerifierPassesOnZeroExit(t *testing.T) {
	root := scratchRepo(t)
	var ranInDir string
	runner := func(_ context.Context, dir string) ([]byte, error) {
		ranInDir = dir
		return []byte("ok"), nil
	}
	v := NewCommandVerifier("command:verify.sh", "sh", "verify.sh", WithScriptRunner(runner))

	res := v.Verify(context.Background(), engine.ProposedDiff{}, engine.Scope{Root: root})
	if !res.Passed {
		t.Fatalf("Verify: want pass on exit 0, got fail: %+v", res.Checks)
	}
	if len(res.Checks) != 1 || res.Checks[0].Name != "command:verify.sh" {
		t.Errorf("CheckResult name = %+v, want the full command: token", res.Checks)
	}
	if ranInDir == root || ranInDir == "" {
		t.Errorf("script ran in %q; must run in a SCRATCH copy, not the original root %q", ranInDir, root)
	}
}

// TestCommandVerifierFailsOnNonZeroExit: a non-zero exit fails the gate WITH the
// script output as the detail (so the skip reason is the real tool error).
func TestCommandVerifierFailsOnNonZeroExit(t *testing.T) {
	root := scratchRepo(t)
	runner := func(_ context.Context, _ string) ([]byte, error) {
		return []byte("go.mod parse error: bad require line"), errors.New("exit status 1")
	}
	v := NewCommandVerifier("command:parse.sh", "sh", "parse.sh", WithScriptRunner(runner))

	res := v.Verify(context.Background(), engine.ProposedDiff{}, engine.Scope{Root: root})
	if res.Passed {
		t.Fatal("Verify: want fail on non-zero exit, got pass")
	}
	if !strings.Contains(res.Checks[0].Detail, "go.mod parse error") {
		t.Errorf("Detail = %q, want the script's stdout surfaced as the reason", res.Checks[0].Detail)
	}
}

// TestCommandVerifierTimeoutFails: a hung script that honours ctx fails the gate
// with a timeout reason rather than hanging the colony.
func TestCommandVerifierTimeoutFails(t *testing.T) {
	root := scratchRepo(t)
	runner := func(ctx context.Context, _ string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	v := NewCommandVerifier("command:slow.sh", "sh", "slow.sh",
		WithScriptRunner(runner), WithCommandVerifyTimeout(20*time.Millisecond))

	res := v.Verify(context.Background(), engine.ProposedDiff{}, engine.Scope{Root: root})
	if res.Passed {
		t.Fatal("Verify: want fail on timeout, got pass")
	}
	if !strings.Contains(res.Checks[0].Detail, "timed out") {
		t.Errorf("Detail = %q, want a timeout reason", res.Checks[0].Detail)
	}
}

// TestScriptFromCheck verifies the command: token parsing the colony + harness use.
func TestScriptFromCheck(t *testing.T) {
	cases := []struct {
		in     string
		script string
		ok     bool
	}{
		{"command:verify.sh", "verify.sh", true},
		{"command:scripts/parse.sh", "scripts/parse.sh", true},
		{"command:", "", false}, // empty suffix is rejected
		{"compile", "", false},  // not a command token
		{"tests:affected", "", false},
	}
	for _, c := range cases {
		got, ok := ScriptFromCheck(c.in)
		if ok != c.ok || got != c.script {
			t.Errorf("ScriptFromCheck(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.script, c.ok)
		}
	}
}
