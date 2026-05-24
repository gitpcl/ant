package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/config"
	"github.com/gitpcl/ant/internal/engine/scout"
)

// TestApplyFailOnGateExitCodes covers the CI gate (feature 4/5): applyFailOn maps
// a scout Result + threshold to the exit-1 findings gate or to clean exit 0. The
// gate is the single place the CLI decides exit 1; everything else routes through
// engine.ExitCode. This asserts the gate decision directly, decoupled from a live
// detector run.
func TestApplyFailOnGateExitCodes(t *testing.T) {
	tests := []struct {
		name      string
		highest   engine.Severity
		threshold engine.Severity
		wantErr   bool
		wantCode  int
	}{
		{"no threshold is clean", engine.SeverityHigh, engine.SeverityUnknown, false, engine.ExitOK},
		{"high meets high gate", engine.SeverityHigh, engine.SeverityHigh, true, engine.ExitFindings},
		{"medium below high gate", engine.SeverityMedium, engine.SeverityHigh, false, engine.ExitOK},
		{"low meets low gate", engine.SeverityLow, engine.SeverityLow, true, engine.ExitFindings},
		{"nothing found, no gate", engine.SeverityUnknown, engine.SeverityHigh, false, engine.ExitOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := applyFailOn(tc.threshold, scout.Result{HighestSeverity: tc.highest})
			if tc.wantErr && err == nil {
				t.Fatalf("expected a gate error for highest=%v threshold=%v", tc.highest, tc.threshold)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected gate error: %v", err)
			}
			// The CLI's centralized translator must map the gate error to exit 1
			// and a clean result to exit 0 — proving codes 0 and 1 in one place.
			if got := translate(err); got != tc.wantCode {
				t.Errorf("translate(%v) = %d, want %d", err, got, tc.wantCode)
			}
		})
	}
}

// translate runs the same classification executeWithExitCode uses, on a single
// error, so a test can assert the centralized mapping without a full command run.
func translate(err error) int {
	if err == nil {
		return engine.ExitOK
	}
	if coder, ok := err.(exitCoder); ok {
		return coder.ExitCode()
	}
	return engine.ExitCode(err)
}

// TestFindingsGateErrorReportsExitOne asserts the typed gate error carries exit
// code 1 (the CI gate band, distinct from operational exit 2).
func TestFindingsGateErrorReportsExitOne(t *testing.T) {
	err := &findingsGateError{highest: engine.SeverityHigh}
	var coder exitCoder = err
	if coder.ExitCode() != engine.ExitFindings {
		t.Errorf("findingsGateError exit = %d, want %d", coder.ExitCode(), engine.ExitFindings)
	}
	if !strings.Contains(err.Error(), "threshold") {
		t.Errorf("gate error message should mention the threshold: %q", err.Error())
	}
}

// TestCentralizedTranslationCoversAllThreeCodes asserts the one translation point
// produces each TECHSPEC §7.1 code from a representative error (feature 5):
// nil → 0, gate → 1, operational → 2.
func TestCentralizedTranslationCoversAllThreeCodes(t *testing.T) {
	cases := map[int]error{
		engine.ExitOK:          nil,
		engine.ExitFindings:    &findingsGateError{highest: engine.SeverityHigh},
		engine.ExitOperational: fmt.Errorf("bad config: %w", engine.ErrOperational),
	}
	for want, err := range cases {
		if got := translate(err); got != want {
			t.Errorf("translate(%v) = %d, want %d", err, got, want)
		}
	}
}

// TestInitExitCodes proves `ant init`'s exit codes through the real command tree:
// a first init succeeds (exit 0); a second without --force fails operationally
// (exit 2) and prints a clear message; --force succeeds again (exit 0). The
// no-force failure routes through the SAME centralized handler as every other
// operational error (config.ErrConfigExists wraps engine.ErrOperational).
func TestInitExitCodes(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/ant.toml"

	out, code := runCmd(t, "init", "--config", cfgPath)
	if code != engine.ExitOK {
		t.Fatalf("first init exit = %d, want 0\n%s", code, out)
	}

	out, code = runCmd(t, "init", "--config", cfgPath)
	if code != engine.ExitOperational {
		t.Fatalf("second init without --force exit = %d, want %d (operational)\n%s", code, engine.ExitOperational, out)
	}
	if !strings.Contains(out, "already exists") {
		t.Errorf("no-force refusal should print a clear message:\n%s", out)
	}

	_, code = runCmd(t, "init", "--config", cfgPath, "--force")
	if code != engine.ExitOK {
		t.Errorf("forced init exit = %d, want 0", code)
	}
}

// TestInitRefusalWrapsOperational asserts at the engine seam that the init
// refusal classifies as operational (exit 2) via the shared sentinel, so the
// centralized handler maps it without any init-specific branch.
func TestInitRefusalWrapsOperational(t *testing.T) {
	if !errors.Is(config.ErrConfigExists, engine.ErrOperational) {
		t.Error("config.ErrConfigExists must wrap engine.ErrOperational so init refusal → exit 2")
	}
}
