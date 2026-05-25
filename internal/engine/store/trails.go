package local

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/gitpcl/ant/internal/engine"
)

// trails.go persists the flag-gated trail markers the colony scheduler reads
// (ADR-0003, TECHSPEC §8.2). Like the per-species trust state (trust.go), it is
// a single JSON file under .ant/state and its methods are a local-store
// convenience that are intentionally NOT on the engine.Store interface: the
// seam the engine consumes is the small colony.TrailStore interface (defined
// where it is used and keyed by the shared engine.TrailKey), which *Store
// satisfies. A future service-backed store — the enterprise shared-trail layer
// (PRD §9) — answers the same two questions differently without changing the
// colony caller.
//
// The state is a count per (species, code-location class), the trail DENSITY the
// scheduler biases on. v1's state is LOCAL and SINGLE-MACHINE only (ADR-0003):
// cross-repo / shared / multi-developer trails are enterprise scope and are not
// represented here. The file survives process restarts, so densities accumulate
// across runs on the same machine — which is what lets a later run bias toward
// classes that earlier runs found fixable.

// trailsFileName is the on-disk file holding the trail-density map, under
// <base>/.ant/state.
const trailsFileName = "trails.json"

// The compile-time assertion that *Store satisfies colony.TrailStore lives in
// the colony wiring (where both packages are already imported), not here:
// asserting it in this package would import colony, and colony's own tests
// import this store — a test-only import cycle. Keying the interface by the
// shared engine.TrailKey lets the store implement it while importing only
// engine, so the dependency stays store → engine, never store → colony.

// trailEntry is one persisted marker: its (species, location-class) key flattened
// to plain string fields plus the accumulated count. A slice of these is the
// on-disk shape (a JSON object key cannot be a struct), kept sorted so the file
// is deterministic across writes.
type trailEntry struct {
	Species       string `json:"species"`
	LocationClass string `json:"locationClass"`
	Count         int    `json:"count"`
}

func (s *Store) trailsFile() string {
	return filepath.Join(s.base, stateDir, trailsFileName)
}

// loadTrails reads the persisted trail entries. A missing file is "no trails
// recorded yet" — an empty slice, not an error — so the first run on a fresh
// repo has zero density and schedules order-stable. The returned slice is a
// fresh copy the caller may mutate.
func (s *Store) loadTrails() ([]trailEntry, error) {
	data, err := os.ReadFile(s.trailsFile())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []trailEntry{}, nil
		}
		return nil, err
	}
	var entries []trailEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// RecordTrail increments the marker count for (species, locationClass),
// persisting it atomically. The colony calls it POST-VERIFY, off the critical
// path (ADR-0003): the verified fix is already staged before this runs, and the
// colony swallows any error this returns — so a write failure costs only a
// scheduling hint, never a fix. It is an immutable update: load, copy, bump,
// rewrite atomically (the same temp-file-then-rename discipline as the rest of
// the store).
func (s *Store) RecordTrail(species, locationClass string) error {
	entries, err := s.loadTrails()
	if err != nil {
		return err
	}
	found := false
	for i := range entries {
		if entries[i].Species == species && entries[i].LocationClass == locationClass {
			entries[i].Count++
			found = true
			break
		}
	}
	if !found {
		entries = append(entries, trailEntry{Species: species, LocationClass: locationClass, Count: 1})
	}
	// Sort so the on-disk file is deterministic regardless of insertion order
	// (stable diffs, reproducible state).
	sortTrailEntries(entries)
	if err := os.MkdirAll(filepath.Join(s.base, stateDir), dirPerm); err != nil {
		return err
	}
	return writeJSON(s.trailsFile(), entries)
}

// TrailDensity returns the current marker counts keyed by (species,
// location-class). The scheduler reads it ONCE before scheduling when trails are
// enabled; an empty map (fresh repo) yields no bias, so scheduling stays
// order-stable. A read error is returned for the scheduler to degrade on (it
// falls back to the stable baseline — a trail read must never break scheduling).
func (s *Store) TrailDensity() (map[engine.TrailKey]int, error) {
	entries, err := s.loadTrails()
	if err != nil {
		return nil, err
	}
	density := make(map[engine.TrailKey]int, len(entries))
	for _, e := range entries {
		density[engine.TrailKey{Species: e.Species, LocationClass: e.LocationClass}] = e.Count
	}
	return density, nil
}

// sortTrailEntries orders entries by species then location class so the persisted
// file is byte-stable across writes (the count never affects ordering).
func sortTrailEntries(entries []trailEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Species != b.Species {
			return a.Species < b.Species
		}
		return a.LocationClass < b.LocationClass
	})
}
