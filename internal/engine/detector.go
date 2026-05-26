package engine

import "context"

// Detector locates findings. It never modifies code (TECHSPEC §5.1).
//
// Built-in adapters: astgrep (default), semgrep, eslint, command (script
// escape hatch). The manifest selects an implementation by named kind; each
// adapter asserts it satisfies this interface at compile time, e.g.:
//
//	var _ engine.Detector = (*astgrepDetector)(nil)
type Detector interface {
	Detect(ctx context.Context, scope Scope) ([]Finding, error)
}

// NamedDetector pairs a Detector with the species that owns it. Scout and the
// colony run loop carry detectors as NamedDetectors so they can honor --ant
// filtering and tag finding provenance by species without reaching into the
// species registry (TECHSPEC §6, §8).
type NamedDetector struct {
	Species  string
	Detector Detector
}

// ScanSafeDetector marks a Detector that is SAFE to run on the read-only `ant
// scout` path — i.e. it performs NO species-supplied script execution. Only
// vetted, embedded detectors (the ast-grep adapter) implement it; the `command`
// script-escape-hatch detector deliberately does NOT.
//
// SECURITY (Sprint 020, defense-in-depth): a `command` detector execs a
// species-supplied script at SCAN time, gated on the resolver's per-species trust
// (species.ScriptExecTrust → TrustDecision.ScriptExecAllowed) — but that gate
// lives on the `ant fix` composition root (colony.BuildRecipes). Scout composes
// its detector set separately (detect.Builtins) and does NOT consult the trust
// resolver, so scout MUST admit only ScanSafe detectors: if a future change ever
// points scout at resolved user/command detectors, an unvetted script could
// otherwise exec at scan time, bypassing the trust gate. Scout enforces this
// invariant by rejecting any detector that is not ScanSafe (fail loud, never
// silently exec) — see scout.Run. The method is a pure marker (no behavior); the
// interface lives here in engine so scout asserts against an INTERFACE, not a
// detect-package concrete type, preserving scout's "depends only on the Detector
// interface" boundary.
type ScanSafeDetector interface {
	Detector
	// ScanSafe reports that this detector runs no species-supplied script and is
	// safe on the read-only scout path. It is a compile-checked marker only.
	ScanSafe() bool
}
