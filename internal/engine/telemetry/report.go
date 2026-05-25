package telemetry

import (
	"sort"
	"time"

	"github.com/gitpcl/ant/internal/engine"
)

// Report is the entire telemetry payload: privacy-safe AGGREGATES and nothing
// else. This struct is the contract surface the privacy test guards — EVERY
// field is a scalar counter, a rate, a species-name→count map, the ant version,
// or a coarse (date-only) timestamp. There is deliberately NO field that could
// carry source code, a diff, a file path, a code snippet, a finding message, a
// repo identifier, or any other PII. A reviewer (and a test) can confirm the
// privacy posture by reading this one struct.
//
// Species NAMES appear as map keys — they are public identifiers (the names of
// built-in/installed species, e.g. "unused-import"), not user data. A file path
// or snippet would be user data and is never present.
type Report struct {
	// AntVersion is the engine library version that produced the metrics. It is
	// a public build identifier, not user data.
	AntVersion string `json:"antVersion"`

	// Date is a COARSE timestamp: the UTC calendar date (YYYY-MM-DD) only. No
	// time-of-day, so it cannot be used as a fine-grained activity fingerprint.
	Date string `json:"date"`

	// SpeciesUsage maps each species NAME to how many findings it produced in
	// the observed run(s): which species are actually used, and how much. Keys
	// are public species identifiers; values are plain counts.
	SpeciesUsage map[string]int `json:"speciesUsage"`

	// AcceptRate is accepted/(accepted+skipped) over `ant review` decisions in
	// the observed run(s): how often a human accepts a proposed fix. It is 0 when
	// no review decisions were observed.
	AcceptRate float64 `json:"acceptRate"`

	// ReviewDecisions is the denominator behind AcceptRate (total review marks
	// observed), so the rate is interpretable rather than a bare ratio.
	ReviewDecisions int `json:"reviewDecisions"`

	// VerifierCatchRate is the share of PROPOSED fixes their own verifier stopped
	// before the fix could reach the user — catches/(verified+skipped). This is
	// the PRD §8 metric that "proves the gate works". It is 0 when no fixes were
	// proposed.
	VerifierCatchRate float64 `json:"verifierCatchRate"`

	// FixesProposed is the denominator behind VerifierCatchRate (verified +
	// skipped), so the catch rate is interpretable.
	FixesProposed int `json:"fixesProposed"`

	// VerifierCatches is the raw count of proposed fixes the verifier gate caught
	// (the numerator of VerifierCatchRate), retained alongside the rate so the
	// PRD §8 signal is captured both as a count and as a share.
	VerifierCatches int `json:"verifierCatches"`
}

// aggregate is the mutable running tally the Sink folds events into. It is
// internal; only the derived Report (rates computed, maps copied) escapes.
type aggregate struct {
	speciesUsage    map[string]int
	fixVerified     int // proposed fixes that passed the gate and were staged
	verifierCatches int // proposed fixes the verifier gate stopped (PRD §8)
	reviewTotal     int
	reviewAccepted  int
}

// newAggregate returns a zeroed aggregate ready to fold into.
func newAggregate() aggregate {
	return aggregate{speciesUsage: make(map[string]int)}
}

// report derives the privacy-safe Report from the running tally at timestamp
// date. Maps are copied so the Report cannot alias the sink's mutable state.
// Rates are guarded against a zero denominator (0, never NaN).
func (a aggregate) report(date string) Report {
	usage := make(map[string]int, len(a.speciesUsage))
	for name, n := range a.speciesUsage {
		usage[name] = n
	}

	// Proposed fixes = those the gate let through (verified) + those the gate
	// stopped (catches). Fixer errors / missing-recipe skips are excluded — no
	// fix was proposed in those cases — so the catch rate reflects only the gate's
	// behavior on real proposed fixes (PRD §8).
	proposed := a.fixVerified + a.verifierCatches
	var catchRate float64
	if proposed > 0 {
		catchRate = float64(a.verifierCatches) / float64(proposed)
	}

	var acceptRate float64
	if a.reviewTotal > 0 {
		acceptRate = float64(a.reviewAccepted) / float64(a.reviewTotal)
	}

	return Report{
		AntVersion:        engine.Version,
		Date:              date,
		SpeciesUsage:      usage,
		AcceptRate:        acceptRate,
		ReviewDecisions:   a.reviewTotal,
		VerifierCatchRate: catchRate,
		FixesProposed:     proposed,
		VerifierCatches:   a.verifierCatches,
	}
}

// SortedSpecies returns the species names in the Report in stable alphabetical
// order — a convenience for deterministic rendering/tests without exposing map
// iteration order. It reads only the public names already in the Report.
func (r Report) SortedSpecies() []string {
	names := make([]string, 0, len(r.SpeciesUsage))
	for name := range r.SpeciesUsage {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// utcDate is the default coarse clock: today's UTC calendar date (YYYY-MM-DD),
// no time-of-day. It is the privacy-conscious default — a date is enough to
// bucket metrics without being a fine-grained activity fingerprint.
func utcDate() string {
	return time.Now().UTC().Format("2006-01-02")
}
