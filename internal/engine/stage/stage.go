// Package stage is the colony's staging area: it holds verified ProposedDiffs
// for a run WITHOUT touching the working tree, persisting them through the
// engine.Store seam (TECHSPEC §3, §8). A staged diff is a recorded intention to
// change code — review (`ant review`) walks it and apply (`ant apply`) lands it;
// nothing here writes source files. Listing returns staged diffs with their
// provenance (the Fixer string and rationale) intact so review can show the
// trust chain.
//
// The package deliberately layers on the existing Store rather than
// reinventing persistence: StageDiff/ListStaged already round-trip
// ProposedDiffs keyed by runID (TECHSPEC §5.4). Area is the thin domain wrapper
// the colony and CLI call, so callers depend on a staging vocabulary
// (Add/List/Clear) rather than on raw Store calls scattered across the codebase.
package stage

import (
	"fmt"

	"github.com/gitpcl/ant/internal/engine"
)

// Area is the staging area for a single colony run. It holds no diffs in
// memory: every Add persists straight through the Store and every List reads
// back from it, so staged state survives a process restart by construction (the
// local Store writes JSON under .ant/state). The working tree is never touched
// — staging records a ProposedDiff, it does not apply one.
type Area struct {
	store engine.Store
	runID string
}

// New returns a staging Area for runID backed by store. The run must already be
// saved in the store before diffs are staged against it (the Store enforces
// this and returns *engine.RunNotFoundError otherwise) so a diff can never be
// staged against a run that was never recorded.
func New(store engine.Store, runID string) *Area {
	return &Area{store: store, runID: runID}
}

// Add stages a verified ProposedDiff for the run. It persists through the Store
// and does NOT modify the working tree — the diff is recorded as a proposal,
// not applied. Provenance (diff.Fixer) and rationale travel with the diff into
// the Store and back out of List unchanged. The diff should carry at least one
// FileDiff and a non-empty Fixer string (provenance is mandatory — TECHSPEC
// §5.2); Add validates this so an unattributed diff cannot be staged.
//
// StageDiff is a read-modify-write append on the Store; concurrent Adds for the
// same run must be serialized by the caller (the colony serializes shared-state
// writes behind its per-project mutex — TECHSPEC §8.1).
func (a *Area) Add(d engine.ProposedDiff) error {
	if d.Fixer == "" {
		return fmt.Errorf("stage: cannot stage a diff with empty provenance (Fixer) for run %q", a.runID)
	}
	if len(d.Files) == 0 {
		return fmt.Errorf("stage: cannot stage an empty diff (no file changes) for run %q", a.runID)
	}
	if err := a.store.StageDiff(a.runID, d); err != nil {
		return fmt.Errorf("stage: persist diff for run %q: %w", a.runID, err)
	}
	return nil
}

// AddRecord stages a full StagedRecord ({Finding, Diff, Verify, Mark}) for the
// run, persisting the provenance triple `ant review` needs alongside the diff
// (review-interaction.md §9). Like Add it does NOT touch the working tree and
// validates that the diff carries provenance and at least one file change, so an
// unattributed or empty record can never be staged. The colony calls this for
// every verified ant because the bus already carries the Finding (ant.start) and
// VerifyResult (ant.verified) at stage time.
func (a *Area) AddRecord(rec engine.StagedRecord) error {
	if rec.Diff.Fixer == "" {
		return fmt.Errorf("stage: cannot stage a record with empty provenance (Fixer) for run %q", a.runID)
	}
	if len(rec.Diff.Files) == 0 {
		return fmt.Errorf("stage: cannot stage an empty diff (no file changes) for run %q", a.runID)
	}
	if err := a.store.StageRecord(a.runID, rec); err != nil {
		return fmt.Errorf("stage: persist record for run %q: %w", a.runID, err)
	}
	return nil
}

// List returns the staged diffs for the run in stage order, each with its
// provenance and rationale intact. An unknown run surfaces the Store's typed
// *engine.RunNotFoundError; a known run with nothing staged returns an empty
// slice and nil error.
func (a *Area) List() ([]engine.ProposedDiff, error) {
	diffs, err := a.store.ListStaged(a.runID)
	if err != nil {
		return nil, fmt.Errorf("stage: list staged diffs for run %q: %w", a.runID, err)
	}
	return diffs, nil
}

// ListRecords returns the full staged records for the run in stage order, with
// provenance (Finding), the trust chain (VerifyResult), and the reviewer's Mark
// intact. `ant review` walks these; `ant apply` filters them by Mark. Surfaces
// the Store's typed *engine.RunNotFoundError for an unknown run.
func (a *Area) ListRecords() ([]engine.StagedRecord, error) {
	records, err := a.store.ListRecords(a.runID)
	if err != nil {
		return nil, fmt.Errorf("stage: list staged records for run %q: %w", a.runID, err)
	}
	return records, nil
}

// Mark persists a reviewer's decision on the staged record at index (its
// position in ListRecords order). It is how `ant review` records accept/skip so
// the decision survives an interrupted session and `ant apply` lands exactly the
// accepted set (review-interaction.md §1, §8).
func (a *Area) Mark(index int, mark engine.Mark) error {
	if err := a.store.SetMark(a.runID, index, mark); err != nil {
		return fmt.Errorf("stage: mark record %d for run %q: %w", index, a.runID, err)
	}
	return nil
}

// Count returns the number of diffs currently staged for the run. It is a
// convenience over List for the colony summary and the run.end aggregate; it
// surfaces the same typed errors List does.
func (a *Area) Count() (int, error) {
	diffs, err := a.List()
	if err != nil {
		return 0, err
	}
	return len(diffs), nil
}
