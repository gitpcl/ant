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
// scout` path. The ast-grep adapter is unconditionally safe (it runs no
// species-supplied script). The `command` script-escape-hatch detector is safe
// ONLY when its caller has cleared the scan-time trust gate and built it with
// detect.WithScanSafe(true) — a vetted built-in or a reviewed installed species;
// an untrusted command detector reports ScanSafe()==false and is rejected.
//
// SECURITY (Sprint 020/022, defense-in-depth): a `command` detector execs a
// species-supplied script at SCAN time, gated on the resolver's per-species trust
// (species.ScriptExecTrust → TrustDecision.ScriptExecAllowed). Both front doors
// now consult that authority: `ant fix` via colony.BuildRecipes, and `ant scout`
// via colony.ScoutDetectors (which takes TrustDecisions and only WithScanSafe-
// marks a command detector whose ScriptExecAllowed is true). Scout still enforces
// the invariant by rejecting any detector that is not ScanSafe (fail loud, never
// silently exec) — see scout.Run — so an UNVETTED script can never reach the
// read-only path even if a future change miswires the composition. The method is
// a pure marker (no behavior); the interface lives here in engine so scout asserts
// against an INTERFACE, not a detect-package concrete type, preserving scout's
// "depends only on the Detector interface" boundary.
type ScanSafeDetector interface {
	Detector
	// ScanSafe reports that this detector runs no species-supplied script and is
	// safe on the read-only scout path. It is a compile-checked marker only.
	ScanSafe() bool
}
