package engine

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCodeClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil is success", nil, ExitOK},
		{"operational sentinel", ErrOperational, ExitOperational},
		{"wrapped operational", fmt.Errorf("bad config: %w", ErrOperational), ExitOperational},
		{"detector unavailable", &DetectorUnavailableError{Detector: "ast-grep", Binary: "ast-grep", Err: errors.New("x")}, ExitOperational},
		{"unknown error is operational", errors.New("boom"), ExitOperational},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExitCode(tc.err); got != tc.want {
				t.Errorf("ExitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

func TestDetectorUnavailableErrorIsOperational(t *testing.T) {
	base := errors.New("executable file not found in $PATH")
	err := &DetectorUnavailableError{Detector: "ast-grep", Binary: "ast-grep", Err: base}

	if !errors.Is(err, ErrOperational) {
		t.Error("DetectorUnavailableError must match ErrOperational (exit code 2)")
	}
	if !errors.Is(err, base) {
		t.Error("Unwrap must expose the underlying exec error")
	}
	var typed *DetectorUnavailableError
	if !errors.As(err, &typed) {
		t.Error("errors.As must recover the concrete *DetectorUnavailableError")
	}
	if msg := err.Error(); msg == "" {
		t.Error("Error() must produce a message naming the missing binary")
	}
}
