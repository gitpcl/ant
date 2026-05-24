// Package local is the v1 Store implementation: pure-Go JSON persistence under
// .ant/state/. It deliberately avoids SQLite (which pulls CGO) so cross-compile,
// arm64, and scratch-Docker builds stay trivial (TECHSPEC §2, §13; Sprint 001
// approach-evaluation note). The Store interface (TECHSPEC §5.4) is the seam a
// service-backed or SQLite store can swap into later without touching callers.
package local

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gitpcl/ant/internal/engine"
)

const (
	// stateDir is the on-disk root for all persisted colony state, relative to
	// the configured base directory (the working-tree root in practice).
	stateDir = ".ant/state"
	runsDir  = "runs"
	stageDir = "staged"

	dirPerm  = 0o755
	filePerm = 0o644
)

// Store persists runs and staged diffs as JSON files under <base>/.ant/state.
// It holds no in-memory state beyond its base path, so a fresh Store reading
// the same base sees everything a previous process wrote — state survives
// restarts by construction.
type Store struct {
	base string // directory containing .ant/state (typically the repo root)
}

// compile-time assertion that Store satisfies the engine.Store interface.
var _ engine.Store = (*Store)(nil)

// New returns a Store rooted at base. base is the directory that will contain
// the .ant/state tree (typically the working-tree root). It does not touch the
// filesystem until the first write.
func New(base string) *Store {
	return &Store{base: base}
}

func (s *Store) runsPath() string  { return filepath.Join(s.base, stateDir, runsDir) }
func (s *Store) stagePath() string { return filepath.Join(s.base, stateDir, stageDir) }

func (s *Store) runFile(id string) string {
	return filepath.Join(s.runsPath(), id+".json")
}

func (s *Store) stageFile(runID string) string {
	return filepath.Join(s.stagePath(), runID+".json")
}

// SaveRun persists a Run, overwriting any prior record with the same id. The
// id must be non-empty.
func (s *Store) SaveRun(run engine.Run) error {
	if run.ID == "" {
		return errors.New("local store: cannot save run with empty ID")
	}
	if err := os.MkdirAll(s.runsPath(), dirPerm); err != nil {
		return fmt.Errorf("local store: create runs dir: %w", err)
	}
	return writeJSON(s.runFile(run.ID), run)
}

// LoadRun reads the Run with the given id. It returns *engine.RunNotFoundError
// (matching errors.Is(err, engine.ErrRunNotFound)) when no such run exists.
func (s *Store) LoadRun(id string) (engine.Run, error) {
	var run engine.Run
	data, err := os.ReadFile(s.runFile(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return engine.Run{}, &engine.RunNotFoundError{ID: id}
		}
		return engine.Run{}, fmt.Errorf("local store: read run %q: %w", id, err)
	}
	if err := json.Unmarshal(data, &run); err != nil {
		return engine.Run{}, fmt.Errorf("local store: decode run %q: %w", id, err)
	}
	return run, nil
}

// StageDiff appends a ProposedDiff to the staged set for runID. The run must
// already exist (it returns *engine.RunNotFoundError otherwise) so diffs cannot
// be staged against a run that was never saved. Appends are read-modify-write
// against the run's stage file; concurrent staging for the same run must be
// serialized by the caller (the colony serializes shared-state writes per
// TECHSPEC §8.1).
func (s *Store) StageDiff(runID string, d engine.ProposedDiff) error {
	if _, err := s.LoadRun(runID); err != nil {
		return err // already a typed RunNotFoundError when the run is missing
	}
	if err := os.MkdirAll(s.stagePath(), dirPerm); err != nil {
		return fmt.Errorf("local store: create stage dir: %w", err)
	}
	existing, err := s.readStaged(runID)
	if err != nil {
		return err
	}
	// Immutable append: build a new slice rather than mutating the loaded one.
	updated := make([]engine.ProposedDiff, 0, len(existing)+1)
	updated = append(updated, existing...)
	updated = append(updated, d)
	return writeJSON(s.stageFile(runID), updated)
}

// ListStaged returns the staged diffs for runID in stage order. The run must
// exist; an unknown run returns *engine.RunNotFoundError. A known run with no
// staged diffs returns an empty slice and nil error.
func (s *Store) ListStaged(runID string) ([]engine.ProposedDiff, error) {
	if _, err := s.LoadRun(runID); err != nil {
		return nil, err
	}
	return s.readStaged(runID)
}

// readStaged reads the stage file for runID, treating a missing file as an
// empty set. It assumes the run's existence has already been validated.
func (s *Store) readStaged(runID string) ([]engine.ProposedDiff, error) {
	data, err := os.ReadFile(s.stageFile(runID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []engine.ProposedDiff{}, nil
		}
		return nil, fmt.Errorf("local store: read staged %q: %w", runID, err)
	}
	var diffs []engine.ProposedDiff
	if err := json.Unmarshal(data, &diffs); err != nil {
		return nil, fmt.Errorf("local store: decode staged %q: %w", runID, err)
	}
	return diffs, nil
}

// writeJSON marshals v to indented JSON and writes it atomically: it writes to
// a temp file in the same directory and renames over the target so a crashed
// write never leaves a half-written state file.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("local store: encode %s: %w", filepath.Base(path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*.json")
	if err != nil {
		return fmt.Errorf("local store: temp file for %s: %w", filepath.Base(path), err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("local store: write %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("local store: close %s: %w", filepath.Base(path), err)
	}
	if err := os.Chmod(tmpName, filePerm); err != nil {
		return fmt.Errorf("local store: chmod %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("local store: rename into %s: %w", filepath.Base(path), err)
	}
	return nil
}
