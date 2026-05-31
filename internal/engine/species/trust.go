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

	// ScriptExecAllowed answers a SECOND, broader trust question the Sprint-020
	// command escape hatch introduced: may this species' command DETECTOR /
	// command: VERIFIER script execute at all? A command detector runs at SCAN
	// time — a wider exec surface than the fix-time tool runner — so an untrusted
	// (OriginUser, never-reviewed) community species must NOT auto-run its script
	// before a human has reviewed it once. Built-in species are vetted at release
	// time and may always run; a user species may run only after one review. This
	// is computed by the SINGLE trust authority (ScriptExecTrust) so the colony
	// composition root only reads a boolean. It is independent of auto-apply: a
	// propose-only built-in still runs its detector (you must detect to propose),
	// while a freshly-installed user species is blocked from scan-time exec.
	ScriptExecAllowed bool
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
			FreshlyInstalled:  r.EffectiveAutoApply && r.Origin == OriginUser && st.FreshlyInstalled(),
			ScriptExecAllowed: ScriptExecTrust(r, st),
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

// ReviewOutcome reports what a single `ant review` pass actually DID, so the
// trust authority can decide whether that pass earned a species its lift of the
// freshly-installed propose-only override. Before Sprint 022 the override lifted
// merely because review.Run returned — i.e. opening and immediately quitting an
// `ant review` session lifted trust without any human judgment. That is too weak
// a signal for a third-party command-detector species, whose scan-time script
// exec is gated behind this very override (ScriptExecTrust). Sprint 022 Finding 6
// tightens it: trust is earned by an EXPLICIT human check, aligning with the
// Sprint 011 trust model.
//
// A pass qualifies as a real review when EITHER:
//   - Decided: the reviewer made at least one explicit accept/skip decision, OR
//   - ReachedEnd: the reviewer walked to the end-of-review screen (they saw every
//     staged item and chose to leave them as-is — still a deliberate human pass).
//
// A pass that does neither (opened then quit on the first item with no decision)
// is NOT a review and must NOT lift the override. The zero value {} is exactly
// that no-op pass — the safe default.
type ReviewOutcome struct {
	// Decided is true once the reviewer made at least one explicit accept/skip
	// decision during the pass.
	Decided bool
	// ReachedEnd is true once the reviewer reached the end-of-review screen,
	// having walked past the last staged item.
	ReachedEnd bool
}

// LiftsTrust reports whether this review pass earned the freshly-installed
// override its lift: an explicit accept/skip decision OR reaching end-of-review.
// A no-decision, never-reached-end pass returns false — the conservative default
// that keeps a third-party command species propose-only / scan-exec-blocked.
func (o ReviewOutcome) LiftsTrust() bool {
	return o.Decided || o.ReachedEnd
}

// MarkReviewedAfter is the SINGLE gated entry point the CLI calls to lift the
// freshly-installed override after an `ant review` pass (Sprint 022 Finding 6).
// It lifts (persists Reviewed=true via store.MarkReviewed) for the named species
// ONLY when the pass's outcome qualifies as a real review (ReviewOutcome.LiftsTrust).
// A no-decision pass is a no-op: trust stays where it was, so a third-party
// command-detector species cannot earn its override lift just by review.Run
// returning. Keeping this decision HERE (the trust authority) rather than in
// cmd/ant means the CLI only passes the outcome signal; the engine owns whether
// it counts. A nil store or empty name set is a no-op. The store's own error is
// returned so callers may choose to treat it as non-fatal (the override simply
// stays conservative — the safe direction).
func MarkReviewedAfter(store TrustStore, outcome ReviewOutcome, names ...string) error {
	if store == nil || len(names) == 0 || !outcome.LiftsTrust() {
		return nil
	}
	return store.MarkReviewed(names...)
}

// ScriptExecTrust is the trust authority for the SECOND question the Sprint-020
// command escape hatch raises: may this species' command DETECTOR / command:
// VERIFIER script EXECUTE? Unlike EffectiveTrust (which gates auto-APPLY and so
// only matters once a diff is produced), this gates EXEC at SCAN time — a
// command detector runs the moment `ant scout`/`ant fix` resolves the species,
// before any human sees output. The rule mirrors the freshly-installed override
// but is INDEPENDENT of auto-apply:
//
//   - Built-in species (OriginBuiltin) are vetted at release time → always allowed
//     (a propose-only built-in still must run its detector to find anything).
//   - User/installed species (OriginUser) may run their script ONLY after one
//     `ant review` pass (state.Reviewed); a brand-new, never-reviewed user
//     species is blocked from scan-time exec regardless of its manifest/ant.toml.
//     This is the property that makes installing a third-party command-detector
//     species safe: its script cannot run on your tree on first sight.
func ScriptExecTrust(r Resolved, state TrustState) bool {
	if r.Origin == OriginBuiltin {
		return true
	}
	return !state.FreshlyInstalled()
}
