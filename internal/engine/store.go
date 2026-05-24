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

	// StageDiff appends a bare ProposedDiff to runID's staged set with no
	// Finding/VerifyResult and Mark=pending. Retained for callers that only have
	// a diff; the colony uses StageRecord to persist the full provenance triple.
	StageDiff(runID string, d ProposedDiff) error
	// ListStaged returns just the ProposedDiffs for runID, projected out of the
	// staged records, in stage order. Retained for diff-only consumers.
	ListStaged(runID string) ([]ProposedDiff, error)

	// StageRecord appends a full StagedRecord ({Finding, Diff, Verify, Mark}) to
	// runID's staged set. This is what `ant fix` persists per verified diff so
	// `ant review` can render provenance and `ant apply` can land marked diffs
	// (review-interaction.md §9). The run must already exist.
	StageRecord(runID string, rec StagedRecord) error
	// ListRecords returns the full staged records for runID in stage order, with
	// provenance and marks intact. `ant review` walks these; `ant apply` filters
	// them by Mark.
	ListRecords(runID string) ([]StagedRecord, error)
	// SetMark persists a reviewer's decision on the staged record at index (its
	// position in ListRecords order) so a decision survives an interrupted review
	// (review-interaction.md §1). An out-of-range index is an error.
	SetMark(runID string, index int, mark Mark) error
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
