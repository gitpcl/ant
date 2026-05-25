package local

import (
	"path/filepath"
	"testing"

	"github.com/gitpcl/ant/internal/engine/species"
)

// TestTrustStoreRoundTrip_FreshOverride proves the per-species trust state
// persists across Store instances (i.e. across process restarts): MarkSeen /
// MarkReviewed written by one Store are read back by a fresh Store at the same
// base. This is the "present on the PREVIOUS run" signal the freshly-installed
// override depends on — it MUST survive a restart.
func TestTrustStoreRoundTrip_FreshOverride(t *testing.T) {
	base := t.TempDir()

	// A brand-new repo: nothing tracked → every species is fresh.
	st := New(base)
	state, err := st.TrustFor("third-party")
	if err != nil {
		t.Fatalf("TrustFor: %v", err)
	}
	if !state.FreshlyInstalled() {
		t.Fatalf("an untracked species must be freshly-installed; got %+v", state)
	}

	// Record seen + reviewed, then read back through a FRESH Store (new process).
	if err := st.MarkSeen("third-party"); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	if err := st.MarkReviewed("third-party"); err != nil {
		t.Fatalf("MarkReviewed: %v", err)
	}

	reopened := New(base)
	got, err := reopened.TrustFor("third-party")
	if err != nil {
		t.Fatalf("TrustFor after reopen: %v", err)
	}
	if !got.Seen || !got.Reviewed {
		t.Errorf("trust state did not survive a restart: got %+v, want {Seen:true Reviewed:true}", got)
	}
	if got.FreshlyInstalled() {
		t.Error("a seen+reviewed species is no longer freshly-installed")
	}
}

// TestTrustStore_SeenWithoutReviewStillFresh asserts MarkSeen alone does not lift
// the override: a seen-but-unreviewed species is still propose-only. Only a
// review pass earns configured trust.
func TestTrustStore_SeenWithoutReviewStillFresh(t *testing.T) {
	st := New(t.TempDir())
	if err := st.MarkSeen("x"); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	got, _ := st.TrustFor("x")
	// Seen flips Seen but not Reviewed; the install/review distinction is preserved
	// so audits can tell "seen but unreviewed" from "never seen".
	if !got.Seen {
		t.Error("MarkSeen should set Seen")
	}
	if got.Reviewed {
		t.Error("MarkSeen must NOT set Reviewed")
	}
	// EffectiveTrust for an installed species in this state is still propose-only.
	r := species.Resolved{Manifest: species.Manifest{Name: "x"}, Origin: species.OriginUser, EffectiveAutoApply: true}
	if species.EffectiveTrust(r, got) {
		t.Error("seen-but-unreviewed installed species must remain propose-only")
	}
}

// TestTrustStore_ClearReArmsOverride asserts ClearTrust drops a species' tracked
// state so a reinstall is treated as brand new again (re-arming the safety
// override) — the `ant species remove` path.
func TestTrustStore_ClearReArmsOverride(t *testing.T) {
	st := New(t.TempDir())
	_ = st.MarkSeen("c")
	_ = st.MarkReviewed("c")
	if err := st.ClearTrust("c"); err != nil {
		t.Fatalf("ClearTrust: %v", err)
	}
	got, _ := st.TrustFor("c")
	if !got.FreshlyInstalled() {
		t.Errorf("after ClearTrust a species must be fresh again; got %+v", got)
	}
}

// TestTrustStore_MultipleSpeciesIndependent asserts marks are per species: seeing
// one does not touch another (trust is never global).
func TestTrustStore_MultipleSpeciesIndependent(t *testing.T) {
	st := New(t.TempDir())
	if err := st.MarkReviewed("a"); err != nil {
		t.Fatalf("MarkReviewed: %v", err)
	}
	a, _ := st.TrustFor("a")
	b, _ := st.TrustFor("b")
	if !a.Reviewed {
		t.Error("species a should be reviewed")
	}
	if b.Reviewed || b.Seen {
		t.Errorf("species b must be untouched by a's mark; got %+v", b)
	}
}

// TestTrustStore_MissingFileIsEmpty asserts a missing state file is an empty map,
// not an error — the first run on a fresh repo reads zero tracked species.
func TestTrustStore_MissingFileIsEmpty(t *testing.T) {
	st := New(filepath.Join(t.TempDir(), "no-state-here"))
	m, err := st.LoadTrust()
	if err != nil {
		t.Fatalf("LoadTrust on fresh repo: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("fresh repo should track no species; got %v", m)
	}
}
