package species

// trust.go is the SINGLE trust-decision authority for the colony (PRD §6.6,
// TECHSPEC §6.3). It answers exactly one question — "may a verified diff from
// this species auto-LAND under --apply?" — and it answers it PER SPECIES. There
// is deliberately no global "trust everything" switch anywhere in the engine;
// trust is granted one species at a time.
//
// The decision layers two things, in order:
//
//  1. Sprint 004 resolution (resolve.go) already computed Resolved.EffectiveAutoApply
//     = ant.toml [species.<name>].auto_apply override, else the manifest default.
//     That is the *configured* trust.
//  2. The freshly-installed RUNTIME override (this file): any species that was
//     NOT present on the previous run is forced propose-only (effective
//     auto-apply = false) until its output has gone through `ant review` once —
//     REGARDLESS of its manifest or ant.toml. This is the property that makes
//     installing a third-party species safe: it cannot auto-land code on its
//     first run with this repo.
//
// Built-in species are exempt from the freshly-installed override: they ship in
// the binary and are vetted as part of the release, so they are trusted from
// first run if their configured auto-apply says so (TECHSPEC §6.3 — the override
// guards THIRD-PARTY/installed species, not the embedded set).

// TrustState is the persisted per-species install/review state the freshly-
// installed override reads. Seen records that the species' folder was present on
// a PREVIOUS run; Reviewed records that its output has gone through `ant review`
// at least once. The zero value ({false, false}) is "brand new, never reviewed"
// — the safe default that forces propose-only.
type TrustState struct {
	// Seen is true once the species was present on a run that has already
	// completed (i.e. not its very first run). A species present for the first
	// time this run is NOT yet Seen. Seen is tracked for audit / `species list`
	// (distinguishing "seen but unreviewed" from "never seen"); it does NOT on
	// its own lift the override — see FreshlyInstalled.
	Seen bool `json:"seen"`
	// Reviewed is true once at least one `ant review` pass has walked this
	// species' staged output. This is the SINGLE condition that lifts the
	// freshly-installed override: after one review, the configured trust applies.
	Reviewed bool `json:"reviewed"`
}

// FreshlyInstalled reports whether the freshly-installed override still applies
// to a species with this state. The spec is precise: a species is forced
// propose-only "until it has been reviewed once" (TECHSPEC §6.3, the second
// trust bullet). So the override lifts ONLY on Reviewed — being merely seen on a
// previous run is not enough. A species that has never been reviewed is treated
// as freshly installed and held propose-only, which is the conservative,
// safe-by-default reading: configured auto-apply for an installed species only
// takes effect after a human has reviewed its output once.
//
// Built-in origin is handled by the caller (EffectiveTrust), which exempts
// built-ins; this method is purely about the persisted runtime state.
func (s TrustState) FreshlyInstalled() bool {
	return !s.Reviewed
}

// TrustStore is the persistence seam the trust decision reads through: the
// per-species install/review state behind the freshly-installed override. It is
// a small interface defined HERE (where it is used) rather than on the global
// engine.Store, so the trust feature owns its own narrow contract and the local
// store satisfies it without bloating the core Store interface (Go idiom: accept
// small interfaces). The local JSON store implements it; tests inject a fake.
//
// LoadTrust returns the full map (used to decide a whole resolved set at once),
// MarkSeen records a species as present after a completed run, and MarkReviewed
// lifts the override after one `ant review` pass.
type TrustStore interface {
	LoadTrust() (map[string]TrustState, error)
	MarkSeen(names ...string) error
	MarkReviewed(names ...string) error
}

// TrustDecision pairs a resolved species with its FINAL effective auto-apply
// after the trust authority has run — the value the colony --apply path gates
// on. Configured is Sprint-004's pre-override auto-apply (kept for provenance /
// `species list` so the UI can show "configured auto-apply, held propose-only
// because freshly installed"); EffectiveAutoApply is the post-override answer.
type TrustDecision struct {
	Resolved           Resolved
	Configured         bool // Sprint-004 ant.toml-or-manifest auto-apply (pre-override)
	EffectiveAutoApply bool // final decision: may a verified diff auto-land?
	FreshlyInstalled   bool // true when the override is actively holding this species propose-only
}

// ResolveTrust is the entry point the CLI composition root calls: given the
// resolved species set and the trust store, it returns the final per-species
// trust decision for each. It reads the persisted state ONCE (LoadTrust) and
// applies EffectiveTrust to every species, so the colony and `species list` see
// one consistent decision. A nil store means "no persistence" — every installed
// species is treated as brand new (the safe default), built-ins keep their
// configured trust.
func ResolveTrust(resolved []Resolved, store TrustStore) ([]TrustDecision, error) {
	state := map[string]TrustState{}
	if store != nil {
		loaded, err := store.LoadTrust()
		if err != nil {
			return nil, err
		}
		state = loaded
	}
	out := make([]TrustDecision, 0, len(resolved))
	for _, r := range resolved {
		st := state[r.Manifest.Name]
		eff := EffectiveTrust(r, st)
		out = append(out, TrustDecision{
			Resolved:           r,
			Configured:         r.EffectiveAutoApply,
			EffectiveAutoApply: eff,
			// The override is actively holding this species only when configured
			// trust WANTED auto-apply but the freshly-installed gate denied it for an
			// installed species.
			FreshlyInstalled: r.EffectiveAutoApply && r.Origin == OriginUser && st.FreshlyInstalled(),
		})
	}
	return out, nil
}

// EffectiveTrust is the single trust-decision authority. Given a resolved
// species (carrying Sprint-004's configured EffectiveAutoApply and its Origin)
// and its persisted TrustState, it returns whether a verified diff from this
// species may AUTO-LAND under --apply.
//
// Rules, in order:
//   - If configured auto-apply is already false, the answer is false. The
//     freshly-installed override can only ever make trust MORE restrictive,
//     never grant it — so there is nothing to compute.
//   - Built-in species (OriginBuiltin) use their configured auto-apply directly:
//     they are vetted at release time and the freshly-installed override does
//     not apply to them (TECHSPEC §6.3).
//   - User/installed species (OriginUser) that are freshly installed (never seen
//     on a previous run, never reviewed) are forced to false — propose-only —
//     regardless of manifest/ant.toml. Once seen-or-reviewed, the configured
//     auto-apply applies.
func EffectiveTrust(r Resolved, state TrustState) bool {
	if !r.EffectiveAutoApply {
		return false // configured propose-only; nothing can grant trust here
	}
	if r.Origin == OriginBuiltin {
		return true // vetted built-in: configured trust stands
	}
	// Installed/user species: gate on the freshly-installed override.
	if state.FreshlyInstalled() {
		return false // first run, never reviewed → forced propose-only
	}
	return true
}
