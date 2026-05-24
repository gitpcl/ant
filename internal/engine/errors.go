package engine

import (
	"errors"
	"fmt"
)

// Exit codes per TECHSPEC §7.1. They are owned by the engine (not cmd/ant) so
// every front door maps the same conditions to the same codes — the CLI just
// returns engine.ExitCode(err) as its process status.
const (
	// ExitOK — success; nothing exceeded the --fail-on threshold.
	ExitOK = 0
	// ExitFindings — findings at or above the --fail-on threshold (CI gate).
	ExitFindings = 1
	// ExitOperational — operational error: bad config, missing detector binary,
	// unreadable scope, etc.
	ExitOperational = 2
)

// ErrOperational is the sentinel every operational-failure error wraps so
// callers can classify "this is an exit-code-2 condition" with errors.Is,
// regardless of the concrete error type (TECHSPEC §7.1).
var ErrOperational = errors.New("engine: operational error")

// DetectorUnavailableError reports that a detector's external binary could not
// be located or started — e.g. `ast-grep` is not installed. Detection is a
// plugin boundary (TECHSPEC §2), so a missing matcher is an operational
// condition (exit code 2), never a panic. It wraps ErrOperational so
// errors.Is(err, engine.ErrOperational) classifies it as exit-code 2.
type DetectorUnavailableError struct {
	Detector string // logical detector name, e.g. "ast-grep"
	Binary   string // executable that could not be run
	Err      error  // underlying exec error
}

func (e *DetectorUnavailableError) Error() string {
	return fmt.Sprintf(
		"detector %q unavailable: cannot run %q (is it installed and on PATH?): %v",
		e.Detector, e.Binary, e.Err,
	)
}

// Unwrap returns the underlying exec error. DetectorUnavailableError also
// matches ErrOperational via Is so a single classifier covers it.
func (e *DetectorUnavailableError) Unwrap() error { return e.Err }

// Is lets errors.Is(err, engine.ErrOperational) succeed for this error so the
// exit-code classifier treats a missing detector binary as exit code 2 without
// importing the concrete type.
func (e *DetectorUnavailableError) Is(target error) bool {
	return target == ErrOperational
}

// ExitCode classifies an error into a process exit code (TECHSPEC §7.1):
//   - nil                       → 0 (success)
//   - operational (ErrOperational, e.g. missing detector binary, bad config) → 2
//   - anything else             → 2 (unknown failures are operational, not a
//     findings gate — only the explicit --fail-on path returns 1, via the
//     caller, not here)
//
// The findings gate (exit 1) is decided by comparing the highest finding
// severity to --fail-on at the render layer, not by an error, so ExitCode only
// distinguishes success from operational failure.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	if errors.Is(err, ErrOperational) {
		return ExitOperational
	}
	return ExitOperational
}
