package fix_test

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/fix"
)

// harnessCtor names a harness adapter and a runner-injecting constructor, so the
// failure-mode tests run identically across claudecode, codex, and pi.
type harnessCtor struct {
	name string
	new  func(cfg fix.HarnessConfig, r fix.CommandRunner) (engine.Fixer, error)
}

func execHarnesses() []harnessCtor {
	return []harnessCtor{
		{"claudecode", fix.NewClaudeCodeWithRunner},
		{"codex", fix.NewCodexWithRunner},
		{"pi", fix.NewPiWithRunner},
	}
}

// TestHarnessMissingBinaryIsCleanSkip proves a missing harness binary returns a
// typed *fix.HarnessUnavailableError (operational, → skip), never a panic
// (TECHSPEC §10). The stub mimics exec's *exec.Error / exec.ErrNotFound.
func TestHarnessMissingBinaryIsCleanSkip(t *testing.T) {
	notFound := func(_ context.Context, binary string, _ []string, _ string) ([]byte, error) {
		return nil, &exec.Error{Name: binary, Err: exec.ErrNotFound}
	}
	for _, h := range execHarnesses() {
		h := h
		t.Run(h.name, func(t *testing.T) {
			fixer, err := h.new(fix.HarnessConfig{Model: "m", Timeout: time.Second}, notFound)
			if err != nil {
				t.Fatalf("construct %s: %v", h.name, err)
			}
			_, ferr := fixer.Fix(context.Background(), adapterTask())
			if ferr == nil {
				t.Fatalf("%s: expected an error when the binary is missing", h.name)
			}
			var unavailable *fix.HarnessUnavailableError
			if !errors.As(ferr, &unavailable) {
				t.Errorf("%s: error = %v, want *HarnessUnavailableError", h.name, ferr)
			}
			// Missing binary is an operational condition (exit code 2).
			if !errors.Is(ferr, engine.ErrOperational) {
				t.Errorf("%s: missing-binary error must classify as operational", h.name)
			}
		})
	}
}

// TestHarnessNonZeroExitIsCleanSkip proves a harness exiting non-zero (e.g. the
// model errored) returns a clean wrapped error → skip, not a crash.
func TestHarnessNonZeroExitIsCleanSkip(t *testing.T) {
	failRun := func(_ context.Context, _ string, _ []string, _ string) ([]byte, error) {
		return nil, errors.New("exit status 1: model refused")
	}
	for _, h := range execHarnesses() {
		h := h
		t.Run(h.name, func(t *testing.T) {
			fixer, err := h.new(fix.HarnessConfig{Model: "m", Timeout: time.Second}, failRun)
			if err != nil {
				t.Fatalf("construct %s: %v", h.name, err)
			}
			if _, ferr := fixer.Fix(context.Background(), adapterTask()); ferr == nil {
				t.Errorf("%s: expected an error on non-zero exit", h.name)
			}
		})
	}
}

// TestHarnessGarbageOutputIsCleanSkip proves unparseable harness output returns
// a clean error → skip, never a panic.
func TestHarnessGarbageOutputIsCleanSkip(t *testing.T) {
	garbage := func(_ context.Context, _ string, _ []string, _ string) ([]byte, error) {
		return []byte("not json at all <<<"), nil
	}
	for _, h := range execHarnesses() {
		h := h
		t.Run(h.name, func(t *testing.T) {
			fixer, err := h.new(fix.HarnessConfig{Model: "m", Timeout: time.Second}, garbage)
			if err != nil {
				t.Fatalf("construct %s: %v", h.name, err)
			}
			if _, ferr := fixer.Fix(context.Background(), adapterTask()); ferr == nil {
				t.Errorf("%s: expected an error on unparseable output", h.name)
			}
		})
	}
}

// TestHarnessRejectsMissingModel proves each adapter refuses construction without
// a configured model rather than silently defaulting to a hardcoded id
// (TECHSPEC §2).
func TestHarnessRejectsMissingModel(t *testing.T) {
	stub := func(_ context.Context, _ string, _ []string, _ string) ([]byte, error) { return nil, nil }
	for _, h := range execHarnesses() {
		h := h
		t.Run(h.name, func(t *testing.T) {
			if _, err := h.new(fix.HarnessConfig{Model: ""}, stub); err == nil {
				t.Errorf("%s: expected an error when model is empty (model must never be hardcoded)", h.name)
			}
		})
	}
}
