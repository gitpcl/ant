package detect

import (
	"context"

	"github.com/gitpcl/ant/internal/engine"
)

// builtinSpecies enumerates the two zero-config deterministic-detection species
// kept as a FALLBACK detector set (TECHSPEC §4, M1/M2). The manifest-driven
// resolver (ResolvedDetectors, Sprint 022) is the primary path scout and fix now
// share; this static table is retained only so a caller without a resolved
// species set still gets a concrete detector set, and so the parity test can
// assert ResolvedDetectors reproduces it for the same two species.
var builtinSpecies = []struct {
	Name string
	Rule string
}{
	{Name: "dead-code", Rule: "dead-code/detect.yml"},
	{Name: "unused-import", Rule: "unused-import/detect.yml"},
}

// Builtins returns the FALLBACK scout detector set: one ast-grep detector per
// built-in species in the static table, paired with the owning species name for
// --ant filtering and provenance. Prefer ResolvedDetectors, which builds the
// detector set from the full resolved manifest set (built-in + installed +
// config-enabled) so scout and fix see identical species. Each detector shells
// out to ast-grep at Detect time; a missing ast-grep binary surfaces as a typed
// *engine.DetectorUnavailableError (exit code 2), never a crash.
//
// rulesRoot is prepended to each species' rule path so the caller controls where
// rule files resolve from (the materialized embedded species tree).
func Builtins(rulesRoot string) []engine.NamedDetector {
	out := make([]engine.NamedDetector, 0, len(builtinSpecies))
	for _, s := range builtinSpecies {
		rule := s.Rule
		if rulesRoot != "" {
			rule = rulesRoot + "/" + s.Rule
		}
		out = append(out, engine.NamedDetector{
			Species:  s.Name,
			Detector: NewASTGrep(s.Name, rule),
		})
	}
	return out
}

// scoutBlockedDetector is the scan-safe stand-in scout uses for a command-detector
// species. It NEVER runs the species' script; instead it reports a single
// informational finding so the species is visible on the read-only scout path
// rather than silently dropped (Sprint 022 deliverable 1). It is ScanSafe by
// construction — it execs nothing — so it passes scout.Run's scan-safe invariant
// without weakening the Sprint-020 trust gate that governs the actual script exec
// on the `ant fix` path.
type scoutBlockedDetector struct{ species string }

// compile-time assertions: the blocked detector is a scan-safe Detector so it is
// admitted on the read-only scout path.
var (
	_ engine.Detector         = scoutBlockedDetector{}
	_ engine.ScanSafeDetector = scoutBlockedDetector{}
)

// NewScoutBlocked builds the scan-safe blocked-until-reviewed detector for a
// command-detector species on the scout path.
func NewScoutBlocked(speciesName string) engine.Detector {
	return scoutBlockedDetector{species: speciesName}
}

// ScanSafe reports true: this detector runs no species-supplied script, so it is
// safe on the read-only scout path (it is the very mechanism that keeps an
// unvetted command script OFF that path).
func (d scoutBlockedDetector) ScanSafe() bool { return true }

// Detect reports a single low-severity informational finding announcing that the
// species' command detector is blocked on the read-only scout path until the
// species is reviewed/run via `ant fix`. It writes nothing and execs nothing.
func (d scoutBlockedDetector) Detect(context.Context, engine.Scope) ([]engine.Finding, error) {
	return []engine.Finding{{
		Species:  d.species,
		Severity: engine.SeverityLow,
		Message:  "command-detector species blocked on scout (read-only): run `ant fix`/`ant review` to clear the scan-time trust gate",
		Meta:     map[string]string{"blocked": "command-detector"},
	}}, nil
}
