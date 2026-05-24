package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
)

// runCmd builds the command tree, captures stdout+stderr, runs it against args,
// and returns the combined output and the exit code the CLI would return.
func runCmd(t *testing.T, args ...string) (string, int) {
	t.Helper()
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	code := executeWithExitCode(root)
	return out.String(), code
}

func TestHelpListsEveryCommand(t *testing.T) {
	out, code := runCmd(t, "--help")
	if code != engine.ExitOK {
		t.Errorf("--help exit = %d, want 0", code)
	}
	// Every command from TECHSPEC §7 must appear in help.
	for _, cmd := range []string{"scout", "fix", "review", "apply", "init", "species"} {
		if !strings.Contains(out, cmd) {
			t.Errorf("--help missing command %q:\n%s", cmd, out)
		}
	}
	// Global flags on the root.
	for _, flag := range []string{"--json", "--fail-on", "--config", "--fixer", "--model", "--concurrency"} {
		if !strings.Contains(out, flag) {
			t.Errorf("--help missing global flag %q:\n%s", flag, out)
		}
	}
}

func TestSpeciesHelpListsChildren(t *testing.T) {
	out, code := runCmd(t, "species", "--help")
	if code != engine.ExitOK {
		t.Errorf("species --help exit = %d, want 0", code)
	}
	for _, child := range []string{"list", "install", "remove"} {
		if !strings.Contains(out, child) {
			t.Errorf("species --help missing child %q:\n%s", child, out)
		}
	}
}

func TestStubCommandsReturnCleanly(t *testing.T) {
	for _, cmd := range []string{"fix", "review", "apply"} {
		out, code := runCmd(t, cmd)
		if code != engine.ExitOK {
			t.Errorf("%s exit = %d, want 0 (clean stub)", cmd, code)
		}
		if !strings.Contains(out, "not yet implemented") {
			t.Errorf("%s should print 'not yet implemented':\n%s", cmd, out)
		}
	}
}

func TestSpeciesStubsReturnCleanly(t *testing.T) {
	for _, args := range [][]string{{"species", "list"}, {"species", "install", "x"}, {"species", "remove", "x"}} {
		out, code := runCmd(t, args...)
		if code != engine.ExitOK {
			t.Errorf("%v exit = %d, want 0", args, code)
		}
		if !strings.Contains(out, "not yet implemented") {
			t.Errorf("%v should print 'not yet implemented':\n%s", args, out)
		}
	}
}

func TestBadSeverityFlagIsOperationalExit(t *testing.T) {
	// --fail-on with a bad value must fail fast at exit 2 (operational, bad
	// input) before any scout run — never trust input past the boundary.
	_, code := runCmd(t, "scout", ".", "--fail-on=bogus")
	if code != engine.ExitOperational {
		t.Errorf("bad --fail-on exit = %d, want %d (operational)", code, engine.ExitOperational)
	}
	_, code = runCmd(t, "scout", ".", "--severity=nope")
	if code != engine.ExitOperational {
		t.Errorf("bad --severity exit = %d, want %d (operational)", code, engine.ExitOperational)
	}
}

func TestBareAntAndScoutShareTheSamePath(t *testing.T) {
	// Bare `ant` is an alias for scout (ADR 0001). Both reach the same handler;
	// with ast-grep absent locally both classify as operational (exit 2) rather
	// than crashing — proving the alias is wired and the missing-binary path is
	// graceful. (When ast-grep is present, both exit 0 on a clean tree.)
	_, bareCode := runCmd(t, "/nonexistent-scope-path-xyz")
	_, scoutCode := runCmd(t, "scout", "/nonexistent-scope-path-xyz")
	if bareCode != scoutCode {
		t.Errorf("bare ant (%d) and scout (%d) diverged; they must share the scout path", bareCode, scoutCode)
	}
}
