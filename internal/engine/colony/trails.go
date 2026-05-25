// Package colony — trails.go is the FLAG-GATED trail system (ADR-0003,
// TECHSPEC §8.2). It is the v1, single-machine, opt-in form: a verified-fixing
// ant may write a trail MARKER keyed by species + code-location class, and the
// scheduler may use trail DENSITY to bias the work queue toward related
// findings. Both behaviors are off by default — with trails off the colony
// schedules exactly as the embarrassingly-parallel worker pool specifies
// (order-stable, no markers written). This file adds NO new event type: trails
// are internal to scheduling, so the frozen --json event contract (ADR-0001
// golden) is untouched.
//
// CONSTRAINT (ADR-0003): trail writes are OFF the critical path. A verified fix
// is staged FIRST (loop.go), and only AFTER staging does an ant record a
// marker; a marker-write failure is swallowed (logged via the returned error,
// never surfaced as a run error) so a trail write can NEVER gate a verified fix
// from being staged.
//
// SCOPE (ADR-0003): cross-repo / shared / multi-developer trails are ENTERPRISE
// concerns (PRD §9) and are NOT built here. The TrailStore the colony consumes
// persists local, single-machine state through the same Store seam the
// enterprise shared-trail layer will later plug into (TECHSPEC §5.4).
package colony

import (
	"path"
	"sort"

	"github.com/gitpcl/ant/internal/engine"
)

// TrailStore is the local persistence seam for trail markers (defined here,
// where it is used, so the colony depends on a 2-method interface, not on the
// concrete *local.Store — mirroring SeenMarker/TrustStore). It is intentionally
// NOT on engine.Store: like trust state, trail state is a local-store concern,
// and a future service-backed store answers the same two questions differently
// without changing this caller (ADR-0003 — the enterprise shared-trail layer
// consumes the same seam).
type TrailStore interface {
	// RecordTrail increments the marker count for (species, locationClass). It is
	// called POST-VERIFY, off the critical path; an error is reported to the
	// caller but never blocks a staged fix.
	RecordTrail(species, locationClass string) error
	// TrailDensity returns the current marker counts keyed by (species,
	// locationClass). The scheduler reads it once before scheduling when trails
	// are enabled; an empty map (fresh repo, no prior verified fixes) means no
	// bias, so scheduling stays order-stable.
	TrailDensity() (map[engine.TrailKey]int, error)
}

// locationClass derives a finding's code-location class: the directory of its
// file. Findings are root-relative with slash separators (the colony/scout
// contract), so path.Dir is correct and OS-independent. A file at the root
// ("repo.go") classes as "." — still a stable class. This is the one place the
// "code-location class" concept is defined, so the writer (RecordTrail) and the
// scheduler (density lookup) agree on the key by construction.
func locationClass(f engine.Finding) string {
	return path.Dir(f.File)
}

// trailKeyOf builds the engine.TrailKey for a finding: its owning species plus
// its location class. Used by both the post-verify marker write and the
// density-bias scheduler so they key markers identically.
func trailKeyOf(f engine.Finding) engine.TrailKey {
	return engine.TrailKey{Species: f.Species, LocationClass: locationClass(f)}
}

// scheduleOrder returns the ants in the order the pool should enqueue them.
//
// With trails OFF (store nil) it returns ants UNCHANGED — the
// embarrassingly-parallel, order-stable behavior v1 ships (ADR-0003). The caller
// (buildAnts) has already sorted findings deterministically, so "unchanged"
// means "today's stable order", byte-for-byte.
//
// With trails ON it STABLE-sorts ants by descending trail density of their
// (species, location-class) key, so findings in classes where this species has
// already produced verified fixes schedule EARLIER — the density bias toward
// related findings (TECHSPEC §8.2). Ties (equal density, including the
// all-zero fresh-repo case) preserve the incoming deterministic order, so an
// empty density map yields exactly the trails-off order: the bias only ever
// re-orders relative to a stable baseline, never randomizes it. A density read
// error degrades to the stable baseline (returns ants unchanged) — a trail read
// must never break scheduling.
func scheduleOrder(ants []Ant, store TrailStore) []Ant {
	if store == nil {
		return ants // trails off: order-stable, no store touched
	}
	density, err := store.TrailDensity()
	if err != nil || len(density) == 0 {
		return ants // unreadable or empty → no bias, stable baseline
	}

	ordered := make([]Ant, len(ants))
	copy(ordered, ants)
	sort.SliceStable(ordered, func(i, j int) bool {
		di := density[trailKeyOf(ordered[i].Finding)]
		dj := density[trailKeyOf(ordered[j].Finding)]
		return di > dj // higher density first; equal density keeps stable order
	})
	return ordered
}
