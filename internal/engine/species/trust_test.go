package species

import (
	"errors"
	"testing"
)

// fakeTrustStore is an in-memory TrustStore for decision tests: it holds the
// per-species state map and records Mark* calls, so a test can drive the
// freshly-installed lifecycle without touching disk.
type fakeTrustStore struct {
	state   map[string]TrustState
	loadErr error
}

func (f *fakeTrustStore) LoadTrust() (map[string]TrustState, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	if f.state == nil {
		f.state = map[string]TrustState{}
	}
	return f.state, nil
}

func (f *fakeTrustStore) MarkSeen(names ...string) error {
	if f.state == nil {
		f.state = map[string]TrustState{}
	}
	for _, n := range names {
		st := f.state[n]
		st.Seen = true
		f.state[n] = st
	}
	return nil
}

func (f *fakeTrustStore) MarkReviewed(names ...string) error {
	if f.state == nil {
		f.state = map[string]TrustState{}
	}
	for _, n := range names {
		st := f.state[n]
		st.Reviewed = true
		f.state[n] = st
	}
	return nil
}

// TestFreshOverride_Spike is the APPROACH-GATE spike (recorded in
// progress_log.md): a species whose manifest says auto_apply=true, that was
// NEVER seen on a previous run and NEVER reviewed, must resolve to effective
// auto-apply FALSE. This is the core safety property — a freshly-installed
// third-party species cannot auto-land on its first run. Proven BEFORE wiring
// the Store persistence and the driver consultation.
func TestFreshOverride_Spike(t *testing.T) {
	// A resolved USER (installed) species whose configured trust is auto-apply
	// true (manifest auto_apply=true, no ant.toml override flipping it off).
	r := Resolved{
		Manifest:           Manifest{Name: "third-party"},
		Origin:             OriginUser,
		EffectiveAutoApply: true, // configured trust says "auto-apply"
	}
	// Brand-new: never seen on a previous run, never reviewed.
	fresh := TrustState{}

	if EffectiveTrust(r, fresh) {
		t.Fatal("freshly-installed species with manifest auto_apply=true must be FORCED propose-only on its first run (EffectiveTrust = false); got true — the core safety property is broken")
	}
}

// ---- Feature 1: per-species trust (never global) ----

// TestTrust_PerSpeciesNeverGlobal asserts the trust decision is computed
// independently per species: in one resolved set a configured-auto-apply
// BUILT-IN auto-applies while a configured-propose-only species does not, and
// flipping one does not affect the other. There is no global switch — each
// species' EffectiveAutoApply is decided from its OWN Resolved + state.
func TestTrust_PerSpeciesNeverGlobal(t *testing.T) {
	resolved := []Resolved{
		{Manifest: Manifest{Name: "unused-import"}, Origin: OriginBuiltin, EffectiveAutoApply: true, EffectiveEnabled: true},
		{Manifest: Manifest{Name: "n+1-query"}, Origin: OriginBuiltin, EffectiveAutoApply: false, EffectiveEnabled: true},
	}
	// Built-ins are exempt from the freshly-installed override, so their
	// configured trust stands directly — isolating the per-species decision.
	decisions, err := ResolveTrust(resolved, &fakeTrustStore{})
	if err != nil {
		t.Fatalf("ResolveTrust: %v", err)
	}
	got := map[string]bool{}
	for _, d := range decisions {
		got[d.Resolved.Manifest.Name] = d.EffectiveAutoApply
	}
	if !got["unused-import"] {
		t.Error("unused-import (configured auto_apply=true, built-in) should auto-apply")
	}
	if got["n+1-query"] {
		t.Error("n+1-query (configured auto_apply=false) must stay propose-only — trust is per species, not global")
	}
}

// TestTrust_AntTomlOverrideCannotBeGranted asserts the override only ever makes
// trust MORE restrictive: a configured-FALSE species can never be granted
// auto-apply by the trust authority regardless of seen/reviewed state. There is
// no path that turns propose-only into auto-apply at the trust layer.
func TestTrust_AntTomlOverrideCannotBeGranted(t *testing.T) {
	r := Resolved{Manifest: Manifest{Name: "x"}, Origin: OriginUser, EffectiveAutoApply: false}
	for _, st := range []TrustState{{}, {Seen: true}, {Reviewed: true}, {Seen: true, Reviewed: true}} {
		if EffectiveTrust(r, st) {
			t.Errorf("configured propose-only species must never auto-apply (state=%+v)", st)
		}
	}
}

// ---- Feature 2: freshly-installed override lifecycle ----

// TestFreshOverride_LiftsAfterReview drives the full lifecycle through
// ResolveTrust + a store: an installed species with manifest auto_apply=true is
// propose-only on first run, STILL propose-only after merely being seen (seen is
// not enough), and finally auto-applies after one review pass.
func TestFreshOverride_LiftsAfterReview(t *testing.T) {
	resolved := []Resolved{{
		Manifest:           Manifest{Name: "third-party"},
		Origin:             OriginUser,
		EffectiveAutoApply: true,
		EffectiveEnabled:   true,
	}}
	store := &fakeTrustStore{}

	decide := func() TrustDecision {
		ds, err := ResolveTrust(resolved, store)
		if err != nil {
			t.Fatalf("ResolveTrust: %v", err)
		}
		return ds[0]
	}

	// 1) First run: brand new → forced propose-only, override active.
	d := decide()
	if d.EffectiveAutoApply {
		t.Fatal("first run: freshly-installed species must be propose-only")
	}
	if !d.FreshlyInstalled {
		t.Error("first run: decision should report the override is actively holding it")
	}
	if !d.Configured {
		t.Error("Configured should preserve the pre-override auto-apply (true) for provenance")
	}

	// 2) Seen on a (now-completed) run, but NOT yet reviewed → still propose-only.
	_ = store.MarkSeen("third-party")
	if decide().EffectiveAutoApply {
		t.Fatal("seen-but-unreviewed species must still be propose-only — only a review pass lifts the override")
	}

	// 3) One review pass → configured trust applies.
	_ = store.MarkReviewed("third-party")
	if !decide().EffectiveAutoApply {
		t.Fatal("after one review pass, the installed species' configured auto_apply=true must take effect")
	}
}

// TestFreshOverride_BuiltinExempt asserts built-in species are NOT subject to
// the freshly-installed override: a built-in with configured auto_apply=true
// auto-applies from its first run (it is vetted at release time — TECHSPEC §6.3).
func TestFreshOverride_BuiltinExempt(t *testing.T) {
	r := Resolved{Manifest: Manifest{Name: "unused-import"}, Origin: OriginBuiltin, EffectiveAutoApply: true}
	if !EffectiveTrust(r, TrustState{}) {
		t.Fatal("a built-in with configured auto_apply=true must auto-apply on first run (override is for installed species only)")
	}
	ds, err := ResolveTrust([]Resolved{r}, &fakeTrustStore{})
	if err != nil {
		t.Fatalf("ResolveTrust: %v", err)
	}
	if ds[0].FreshlyInstalled {
		t.Error("a built-in is never reported as freshly-installed-held")
	}
}

// TestFreshOverride_NilStoreTreatsInstalledAsFresh asserts the safe default when
// no persistence is available: installed species are treated as brand new
// (propose-only), built-ins keep their configured trust.
func TestFreshOverride_NilStoreTreatsInstalledAsFresh(t *testing.T) {
	resolved := []Resolved{
		{Manifest: Manifest{Name: "installed"}, Origin: OriginUser, EffectiveAutoApply: true},
		{Manifest: Manifest{Name: "builtin"}, Origin: OriginBuiltin, EffectiveAutoApply: true},
	}
	ds, err := ResolveTrust(resolved, nil)
	if err != nil {
		t.Fatalf("ResolveTrust(nil store): %v", err)
	}
	byName := map[string]bool{}
	for _, d := range ds {
		byName[d.Resolved.Manifest.Name] = d.EffectiveAutoApply
	}
	if byName["installed"] {
		t.Error("with no trust persistence, an installed species must be propose-only (safe default)")
	}
	if !byName["builtin"] {
		t.Error("a built-in keeps its configured trust even with no persistence")
	}
}

// TestFreshOverride_StoreLoadErrorSurfaces asserts a trust-state load failure is
// returned (operational), not silently treated as "all fresh" — a corrupt state
// file should fail loudly rather than guess.
func TestFreshOverride_StoreLoadErrorSurfaces(t *testing.T) {
	boom := errors.New("corrupt trust file")
	_, err := ResolveTrust([]Resolved{{Manifest: Manifest{Name: "x"}}}, &fakeTrustStore{loadErr: boom})
	if !errors.Is(err, boom) {
		t.Fatalf("ResolveTrust should surface the load error; got %v", err)
	}
}
