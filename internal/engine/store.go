package engine

import (
	"errors"
	"fmt"
)

// Store persists colony state. A local JSON implementation ships in v1; the
// interface is the seam the enterprise service-backed store plugs into
// (TECHSPEC §5.4). Trail state is optional in v1 and not part of this surface.
//
// Implementations assert interface satisfaction at compile time, e.g.:
//
//	var _ engine.Store = (*local.Store)(nil)
type Store interface {
	SaveRun(run Run) error
	LoadRun(id string) (Run, error)
	StageDiff(runID string, d ProposedDiff) error
	ListStaged(runID string) ([]ProposedDiff, error)
}

// ErrRunNotFound is the typed error returned by Store.LoadRun (and by
// ListStaged for an unknown run) when no run exists for the given id. Callers
// match it with errors.Is to distinguish "no such run" from an I/O failure.
var ErrRunNotFound = errors.New("engine: run not found")

// RunNotFoundError wraps ErrRunNotFound with the offending run id so the
// message names the run while errors.Is(err, ErrRunNotFound) still matches.
type RunNotFoundError struct {
	ID string
}

func (e *RunNotFoundError) Error() string {
	return fmt.Sprintf("engine: run %q not found", e.ID)
}

// Unwrap lets errors.Is(err, ErrRunNotFound) succeed against a RunNotFoundError.
func (e *RunNotFoundError) Unwrap() error {
	return ErrRunNotFound
}
