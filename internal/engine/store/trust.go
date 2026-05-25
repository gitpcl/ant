package local

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/gitpcl/ant/internal/engine/species"
)

// trust.go persists the per-species install/review state the freshly-installed
// trust override reads (Sprint 011, TECHSPEC §6.3). It is a single JSON map
// under .ant/state keyed by species name. Like LatestRunID, these methods are a
// local-store convenience and are intentionally NOT on the engine.Store
// interface: the trust seam the engine consumes is the small species.TrustStore
// interface (defined where it is used), which *Store satisfies. A future
// service-backed store answers the same questions differently without changing
// callers.
//
// The state map records species.TrustState ({Seen, Reviewed}) per name. A
// species absent from the map is brand new ({false, false}) — the safe default
// that forces propose-only. The file survives process restarts (it is the
// "present on the PREVIOUS run" signal), so it must be read at the start of a
// run BEFORE the current run is recorded as seen.

// trustFileName is the on-disk file holding the per-species trust state map,
// under <base>/.ant/state.
const trustFileName = "species-trust.json"

// compile-time assertion that *Store satisfies the trust seam the engine reads
// through (species.TrustStore). Keeping the interface in the species package
// (defined where used) and asserting satisfaction here keeps the dependency
// pointing store → species, never the reverse.
var _ species.TrustStore = (*Store)(nil)

func (s *Store) trustFile() string {
	return filepath.Join(s.base, stateDir, trustFileName)
}

// LoadTrust reads the persisted per-species trust state map. A missing file is
// "no species tracked yet" — an empty map, not an error — so the first run on a
// fresh repo treats every species as brand new (forced propose-only for
// installed species). The returned map is a fresh copy the caller may mutate.
func (s *Store) LoadTrust() (map[string]species.TrustState, error) {
	data, err := os.ReadFile(s.trustFile())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]species.TrustState{}, nil
		}
		return nil, err
	}
	var m map[string]species.TrustState
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]species.TrustState{}
	}
	return m, nil
}

// TrustFor returns the persisted TrustState for a single species. An untracked
// species (never seen, never reviewed) returns the zero value, which
// FreshlyInstalled() reports as true — the safe default.
func (s *Store) TrustFor(name string) (species.TrustState, error) {
	m, err := s.LoadTrust()
	if err != nil {
		return species.TrustState{}, err
	}
	return m[name], nil
}

// MarkSeen records that the named species was present on a run that has now
// happened, so on the NEXT run TrustState.Seen is true. `ant fix` calls this for
// every enabled species AFTER the run's findings are persisted, so a species is
// only "seen on a previous run" once at least one run has completed with it
// present. It is an immutable update: load, copy, set, rewrite atomically.
//
// MarkSeen does NOT lift the freshly-installed override on its own — being seen
// once is "no longer brand new", but the override that forces propose-only is
// only fully lifted by a review pass (MarkReviewed). Seen is tracked separately
// so `species list` and audits can distinguish "seen but unreviewed" from "never
// seen".
func (s *Store) MarkSeen(names ...string) error {
	return s.update(func(m map[string]species.TrustState) {
		for _, name := range names {
			st := m[name]
			st.Seen = true
			m[name] = st
		}
	})
}

// MarkReviewed records that the named species' output has gone through `ant
// review` once, lifting the freshly-installed override so the species' CONFIGURED
// trust applies on subsequent runs. `ant review` calls this for the species that
// owned the staged records it walked. Immutable update.
func (s *Store) MarkReviewed(names ...string) error {
	return s.update(func(m map[string]species.TrustState) {
		for _, name := range names {
			st := m[name]
			st.Reviewed = true
			m[name] = st
		}
	})
}

// ClearTrust removes a species' tracked state entirely so a later reinstall is
// treated as brand new again (forced propose-only). `ant species remove` calls
// this so removing then reinstalling a community species re-arms the safety
// override rather than inheriting stale "reviewed" trust.
func (s *Store) ClearTrust(name string) error {
	return s.update(func(m map[string]species.TrustState) {
		delete(m, name)
	})
}

// update applies mutate to a freshly-loaded copy of the trust map and writes it
// back atomically (same temp-file-then-rename discipline as the rest of the
// store). It serializes the read-modify-write so a single process's calls do not
// interleave; concurrent processes are not expected for state writes (the colony
// records seen once, post-run, on the main goroutine).
func (s *Store) update(mutate func(map[string]species.TrustState)) error {
	m, err := s.LoadTrust()
	if err != nil {
		return err
	}
	mutate(m)
	if err := os.MkdirAll(filepath.Join(s.base, stateDir), dirPerm); err != nil {
		return err
	}
	// json.Marshal sorts map keys lexicographically, so the on-disk file is
	// deterministic across writes without an explicit sort.
	return writeJSON(s.trustFile(), m)
}
