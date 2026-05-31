// Package explain implements `ant explain <run>|<finding>`: a non-TUI command
// that loads a run (or a single finding within a run) from the Store and renders
// its detail for CI and front-door integrations (Sprint 022 missing feature).
// All logic lives here; cmd/ant only parses the argument, calls Resolve, and
// renders (TECHSPEC §3).
//
// Identity model: a run is addressed by its Store ID (e.g. "fix-1717f…"). A
// finding has no standalone persisted ID — findings live positionally inside a
// run's Findings slice — so a finding is addressed as "<runID>#<index>", a
// 0-based index into that run's findings. This reuses the existing Store
// (LoadRun) and engine.Run/engine.Finding types verbatim; it does NOT invent a
// new persistence path.
package explain

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gitpcl/ant/internal/engine"
)

// findingSep separates a run ID from a finding index in a finding reference:
// "<runID>#<index>". '#' cannot appear in a generated run ID ("fix-<nanos>")
// and is not a shell-special character, so it is a safe, unambiguous delimiter.
const findingSep = "#"

// RunLoader is the slice of the Store interface explain needs: it loads a run by
// ID. *store.Store (the local JSON store) satisfies it, as does any future
// service-backed store. Defined here, where it is consumed, per the Go idiom of
// small interfaces at the point of use.
type RunLoader interface {
	LoadRun(id string) (engine.Run, error)
}

// Kind tags which detail a resolution produced so the renderer and JSON
// consumers can branch without inspecting nil-ness of the embedded values.
type Kind string

const (
	// KindRun — the reference resolved to a whole run.
	KindRun Kind = "run"
	// KindFinding — the reference resolved to a single finding within a run.
	KindFinding Kind = "finding"
)

// Detail is the resolved explain result: either a whole run or one finding
// within a run, tagged by Kind. It is the --json contract for CI integrations —
// a single self-contained JSON document (like doctor / species list), not a
// bus event stream. Index is the finding's 0-based position within Run.Findings
// and is meaningful only when Kind == KindFinding.
type Detail struct {
	Kind    Kind            `json:"kind"`
	Ref     string          `json:"ref"`               // the reference as the user gave it
	RunID   string          `json:"runId"`             // the owning run's ID
	Index   int             `json:"index,omitempty"`   // finding index (KindFinding only)
	Run     *engine.Run     `json:"run,omitempty"`     // populated for KindRun
	Finding *engine.Finding `json:"finding,omitempty"` // populated for KindFinding
}

// ErrBadFindingRef is returned when a "<runID>#<index>" reference is malformed
// (empty run id, non-integer index) or the index is out of range for the run.
// It wraps engine.ErrOperational so the CLI's centralized classifier maps it to
// exit code 2 — a bad argument is an operational failure, the same class as a
// malformed config the run path rejects.
var ErrBadFindingRef = fmt.Errorf("%w: explain: invalid finding reference", engine.ErrOperational)

// Resolve loads the detail for ref from the store. A ref containing '#' is a
// finding reference ("<runID>#<index>"); otherwise it is a run ID. A missing run
// surfaces the store's typed *engine.RunNotFoundError (errors.Is ErrRunNotFound,
// classified as exit 2); a malformed or out-of-range finding reference returns
// ErrBadFindingRef. The store is never written — explain is read-only.
func Resolve(loader RunLoader, ref string) (Detail, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Detail{}, fmt.Errorf("%w: empty reference", ErrBadFindingRef)
	}

	runID, idxStr, isFinding := strings.Cut(ref, findingSep)
	if !isFinding {
		return resolveRun(loader, ref)
	}
	return resolveFinding(loader, ref, runID, idxStr)
}

// resolveRun loads a whole run by ID.
func resolveRun(loader RunLoader, runID string) (Detail, error) {
	run, err := loader.LoadRun(runID)
	if err != nil {
		return Detail{}, err // already a typed RunNotFoundError when missing
	}
	r := run // copy so the returned pointer is independent of the caller's value
	return Detail{Kind: KindRun, Ref: runID, RunID: runID, Run: &r}, nil
}

// resolveFinding loads a run and projects out the finding at the given index.
func resolveFinding(loader RunLoader, ref, runID, idxStr string) (Detail, error) {
	if runID == "" {
		return Detail{}, fmt.Errorf("%w %q: run id is empty", ErrBadFindingRef, ref)
	}
	index, perr := strconv.Atoi(strings.TrimSpace(idxStr))
	if perr != nil {
		return Detail{}, fmt.Errorf("%w %q: index %q is not an integer", ErrBadFindingRef, ref, idxStr)
	}
	run, err := loader.LoadRun(runID)
	if err != nil {
		return Detail{}, err
	}
	if index < 0 || index >= len(run.Findings) {
		return Detail{}, fmt.Errorf("%w %q: index %d out of range (run has %d findings)", ErrBadFindingRef, ref, index, len(run.Findings))
	}
	f := run.Findings[index] // copy so the returned pointer is independent
	return Detail{Kind: KindFinding, Ref: ref, RunID: runID, Index: index, Finding: &f}, nil
}

// IsBadFindingRef reports whether err is a malformed/out-of-range finding
// reference (as opposed to a missing run). Both map to exit 2, but the CLI may
// want to phrase them differently; this keeps the test of intent in one place.
func IsBadFindingRef(err error) bool {
	return errors.Is(err, ErrBadFindingRef)
}
