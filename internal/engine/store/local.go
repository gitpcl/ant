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
	"strings"

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

// LatestRunID returns the id of the most recently saved run (by file modtime),
// or "" when no run has been recorded. It is a CLI convenience so `ant review`
// with no argument reviews the run `ant fix` just produced; it is NOT on the
// Store interface (a service-backed store would answer this differently). A
// missing runs directory is "no runs", not an error.
func (s *Store) LatestRunID() (string, error) {
	entries, err := os.ReadDir(s.runsPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("local store: list runs: %w", err)
	}
	latestID := ""
	var latestMod int64 = -1
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		if mod := info.ModTime().UnixNano(); mod > latestMod {
			latestMod = mod
			latestID = strings.TrimSuffix(e.Name(), ".json")
		}
	}
	return latestID, nil
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

// StageDiff appends a bare ProposedDiff (no Finding/Verify, Mark=pending) to the
// staged set for runID. It is a thin shim over StageRecord so the on-disk format
// is a single records file; diff-only callers keep working unchanged.
func (s *Store) StageDiff(runID string, d engine.ProposedDiff) error {
	return s.StageRecord(runID, engine.StagedRecord{Diff: d, Mark: engine.MarkPending})
}

// StageRecord appends a full StagedRecord to the staged set for runID. The run
// must already exist (it returns *engine.RunNotFoundError otherwise) so records
// cannot be staged against a run that was never saved. Appends are
// read-modify-write against the run's stage file; concurrent staging for the
// same run must be serialized by the caller (the colony serializes shared-state
// writes per TECHSPEC §8.1).
func (s *Store) StageRecord(runID string, rec engine.StagedRecord) error {
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
	updated := make([]engine.StagedRecord, 0, len(existing)+1)
	updated = append(updated, existing...)
	updated = append(updated, rec)
	return writeJSON(s.stageFile(runID), updated)
}

// ListStaged returns the staged diffs for runID in stage order, projected out of
// the staged records. The run must exist; an unknown run returns
// *engine.RunNotFoundError. A known run with no staged diffs returns an empty
// slice and nil error.
func (s *Store) ListStaged(runID string) ([]engine.ProposedDiff, error) {
	records, err := s.ListRecords(runID)
	if err != nil {
		return nil, err
	}
	diffs := make([]engine.ProposedDiff, 0, len(records))
	for _, rec := range records {
		diffs = append(diffs, rec.Diff)
	}
	return diffs, nil
}

// ListRecords returns the full staged records for runID in stage order. The run
// must exist; an unknown run returns *engine.RunNotFoundError. A known run with
// nothing staged returns an empty slice and nil error.
func (s *Store) ListRecords(runID string) ([]engine.StagedRecord, error) {
	if _, err := s.LoadRun(runID); err != nil {
		return nil, err
	}
	return s.readStaged(runID)
}

// SetMark persists a reviewer's decision on the staged record at index. It is a
// read-modify-write against the records file: load, replace the mark on a copy
// of the slice (immutable update — never mutate the loaded slice in place), and
// rewrite atomically. An out-of-range index is an error so a bad cursor cannot
// silently corrupt the set.
func (s *Store) SetMark(runID string, index int, mark engine.Mark) error {
	records, err := s.ListRecords(runID)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(records) {
		return fmt.Errorf("local store: set mark for run %q: index %d out of range (have %d staged)", runID, index, len(records))
	}
	updated := make([]engine.StagedRecord, len(records))
	copy(updated, records)
	updated[index].Mark = mark
	return writeJSON(s.stageFile(runID), updated)
}

// readStaged reads the stage file for runID, treating a missing file as an
// empty set. It assumes the run's existence has already been validated.
func (s *Store) readStaged(runID string) ([]engine.StagedRecord, error) {
	data, err := os.ReadFile(s.stageFile(runID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []engine.StagedRecord{}, nil
		}
		return nil, fmt.Errorf("local store: read staged %q: %w", runID, err)
	}
	var records []engine.StagedRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("local store: decode staged %q: %w", runID, err)
	}
	return records, nil
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
