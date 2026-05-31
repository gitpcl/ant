package local

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gitpcl/ant/internal/engine/species"
)

// trust.go persists the per-species install/review state the freshly-installed
// trust override reads (Sprint 011, TECHSPEC §6.3). It is a single JSON map
// keyed by species name. Like LatestRunID, these methods are a local-store
// convenience and are intentionally NOT on the engine.Store interface: the trust
// seam the engine consumes is the small species.TrustStore interface (defined
// where it is used), which *Store satisfies. A future service-backed store
// answers the same questions differently without changing callers.
//
// The state map records species.TrustState ({Seen, Reviewed}) per name. A
// species absent from the map is brand new ({false, false}) — the safe default
// that forces propose-only. The file survives process restarts (it is the
// "present on the PREVIOUS run" signal), so it must be read at the start of a
// run BEFORE the current run is recorded as seen.
//
// SECURITY (Sprint 022 follow-up): trust state lives in a USER-LOCAL directory
// (os.UserConfigDir()/ant/trust, overridable via ANT_TRUST_HOME), NOT inside the
// scanned repo. It used to live at <base>/.ant/state, which let a scanned repo
// ship its own species-trust.json pre-asserting reviewed=true and thereby grant
// its OWN command-detector script scan-time exec — a trust-confusion vector once
// `ant scout`/`ant fix` resolve species + trust relative to a target path. Keying
// the file by a hash of the repo's ABSOLUTE path means a foreign checkout cannot
// self-assert trust; review state is bound to where the repo lives on THIS
// machine and travels with the user, not with the (untrusted) tree.

// trustFileName is the legacy on-disk file name; retained only for the per-repo
// file's extension. The file now lives under the user-local trust root, named by
// the repo key (see trustFile).
const trustFileName = "species-trust.json"

// trustHomeEnv overrides the user-local trust root (tests set it to a TempDir so
// they never touch the real user config dir).
const trustHomeEnv = "ANT_TRUST_HOME"

// compile-time assertion that *Store satisfies the trust seam the engine reads
// through (species.TrustStore). Keeping the interface in the species package
// (defined where used) and asserting satisfaction here keeps the dependency
// pointing store → species, never the reverse.
var _ species.TrustStore = (*Store)(nil)

// trustRoot resolves the user-local directory that holds every repo's trust
// file: ANT_TRUST_HOME if set, else <os.UserConfigDir>/ant/trust. It is outside
// any scanned repo by construction, so a target tree cannot supply its own trust
// state.
func trustRoot() (string, error) {
	if env := os.Getenv(trustHomeEnv); env != "" {
		return env, nil
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("local store: resolve user config dir for trust state (set %s to override): %w", trustHomeEnv, err)
	}
	return filepath.Join(cfg, "ant", "trust"), nil
}

// repoKey derives the per-repo trust file's base name from the store's base
// directory: the hex SHA-256 of its absolute, cleaned path. Hashing the absolute
// path means `ant fix .` and `ant fix /abs/repo` (same tree) share one trust
// file, while two different repos never collide — and the scanned tree's own
// contents never influence the key.
func (s *Store) repoKey() string {
	abs, err := filepath.Abs(s.base)
	if err != nil {
		abs = filepath.Clean(s.base) // Abs only fails if cwd is unreadable; Clean is a safe fallback
	}
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:])
}

// trustFile returns the absolute path to this repo's trust file under the
// user-local trust root. It errors only if the trust root cannot be resolved.
func (s *Store) trustFile() (string, error) {
	root, err := trustRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, s.repoKey()+"-"+trustFileName), nil
}

// LoadTrust reads the persisted per-species trust state map. A missing file is
// "no species tracked yet" — an empty map, not an error — so the first run on a
// fresh repo treats every species as brand new (forced propose-only for
// installed species). The returned map is a fresh copy the caller may mutate.
func (s *Store) LoadTrust() (map[string]species.TrustState, error) {
	path, err := s.trustFile()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
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
	path, err := s.trustFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return err
	}
	// json.Marshal sorts map keys lexicographically, so the on-disk file is
	// deterministic across writes without an explicit sort.
	return writeJSON(path, m)
}
