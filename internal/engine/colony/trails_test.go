package colony_test

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/colony"
	"github.com/gitpcl/ant/internal/engine/events"
	local "github.com/gitpcl/ant/internal/engine/store"
)

// compile-time assertion that the local Store satisfies the colony.TrailStore
// seam. It lives in this test file (not the store package) because the store's
// own tests import nothing of colony, while asserting it inside the store
// package would create a store → colony → (test) store import cycle. Keying
// TrailStore by the shared engine.TrailKey is what lets the store implement it
// importing only engine.
var _ colony.TrailStore = (*local.Store)(nil)

// fakeTrailStore is an in-memory TrailStore for precise scheduler-bias control.
// It is concurrency-safe (the colony writes markers under its serialize lock,
// but -race still inspects the map) and records both densities (seeded by the
// test) and the markers RecordTrail wrote (asserted by the test). recordErr,
// when set, makes RecordTrail fail — used to prove a failing trail write never
// gates a verified fix.
type fakeTrailStore struct {
	mu        sync.Mutex
	density   map[engine.TrailKey]int
	recorded  map[engine.TrailKey]int
	recordErr error
	reads     int
}

func newFakeTrailStore() *fakeTrailStore {
	return &fakeTrailStore{
		density:  map[engine.TrailKey]int{},
		recorded: map[engine.TrailKey]int{},
	}
}

func (f *fakeTrailStore) seed(species, locationClass string, count int) {
	f.density[engine.TrailKey{Species: species, LocationClass: locationClass}] = count
}

func (f *fakeTrailStore) RecordTrail(species, locationClass string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.recordErr != nil {
		return f.recordErr
	}
	f.recorded[engine.TrailKey{Species: species, LocationClass: locationClass}]++
	return nil
}

func (f *fakeTrailStore) TrailDensity() (map[engine.TrailKey]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	// Return a copy so the scheduler cannot mutate the seeded state.
	out := make(map[engine.TrailKey]int, len(f.density))
	for k, v := range f.density {
		out[k] = v
	}
	return out, nil
}

// findingAt builds a finding for a specific species + file (the file's directory
// is its code-location class). Used to construct findings across distinct
// classes so trail-density bias is observable in the schedule order.
func findingAt(species, file string, line int) engine.Finding {
	return engine.Finding{
		Species:  species,
		File:     file,
		Span:     engine.Span{StartLine: line, EndLine: line},
		Severity: engine.SeverityLow,
		Message:  "finding",
		Snippet:  "x",
	}
}

// scheduleOrderFromEvents reconstructs the order ants were SCHEDULED in from the
// event stream. antID is the queue position (pool assigns antID = i+1 in
// enqueue order), so ordering antIDs ascending and reading each ant.start's
// finding file yields the exact schedule order — the observable the trails tests
// assert on.
func scheduleOrderFromEvents(evs []events.Event) []string {
	type idFile struct {
		id   int
		file string
	}
	var seen []idFile
	for _, e := range evs {
		if e.Type == events.TypeAntStart && e.AntStart != nil {
			seen = append(seen, idFile{id: e.AntStart.AntID, file: e.AntStart.Finding.File})
		}
	}
	sort.Slice(seen, func(i, j int) bool { return seen[i].id < seen[j].id })
	order := make([]string, len(seen))
	for i, s := range seen {
		order[i] = s.file
	}
	return order
}

// runOrderedAnts runs the colony serially (Concurrency 1) over the given ants
// with the given (possibly nil) trail store, and returns the schedule order
// reconstructed from the event stream. Concurrency 1 makes the schedule order
// deterministic so the ordering assertion is exact, while a separate -race test
// covers the concurrent path.
func runOrderedAnts(t *testing.T, runID string, ants []colony.Ant, trails colony.TrailStore) []string {
	t.Helper()
	st := newRun(t, runID)
	bus := events.NewBus(events.WithBuffer(4 * (len(ants) + 2)))
	sub := bus.Subscribe()
	evDone := collect(sub)

	_, err := colony.Run(context.Background(), bus, colony.Options{
		Scope:       engine.Scope{Root: "."},
		Ants:        ants,
		Store:       st,
		RunID:       runID,
		Concurrency: 1,
		Trails:      trails,
	})
	bus.Close()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return scheduleOrderFromEvents(<-evDone)
}

// orderedAnts builds ants for the given files (all one species), preserving the
// caller's order — buildAnts/colony.Run will sort findings deterministically, so
// the input here is already in deterministic (file-sorted) order to mirror what
// the driver produces.
func orderedAnts(fixer engine.Fixer, files ...string) []colony.Ant {
	ants := make([]colony.Ant, len(files))
	for i, f := range files {
		ants[i] = colony.Ant{
			Finding:  findingAt("demo", f, i+1),
			Fixer:    fixer,
			Verifier: fakePassVerifier{},
		}
	}
	return ants
}

// TestTrailsOff_ScheduleIsOrderStable is the APPROACH-GATE validation (run
// FIRST, before the density bias): with trails OFF (Trails nil) the schedule
// order is identical to the input order — the embarrassingly-parallel,
// order-stable behavior v1 ships (ADR-0003). Two distinct input orders each come
// out unchanged, proving the trails-off path applies NO reordering.
func TestTrailsOff_ScheduleIsOrderStable(t *testing.T) {
	fixer := &fakeFixer{}
	files := []string{"pkg-a/x.go", "pkg-b/y.go", "pkg-c/z.go"}

	got := runOrderedAnts(t, "off-stable", orderedAnts(fixer, files...), nil)
	if !equalOrder(got, files) {
		t.Errorf("trails off: schedule order = %v, want input order %v (order-stable)", got, files)
	}

	// A different input order must likewise pass through unchanged.
	rev := []string{"pkg-c/z.go", "pkg-b/y.go", "pkg-a/x.go"}
	got2 := runOrderedAnts(t, "off-stable-2", orderedAnts(fixer, rev...), nil)
	if !equalOrder(got2, rev) {
		t.Errorf("trails off: schedule order = %v, want input order %v (no reorder)", got2, rev)
	}
}

// TestTrailsOff_WritesNoMarkers proves that with trails OFF the colony never
// touches the trail store — no density read, no marker write — even though every
// ant produces a verified, staged fix. (The store here is a fake passed as nil
// to Options, so the colony cannot reach it; this test asserts the nil path is a
// true no-op, complementing the persistence test below.)
func TestTrailsOff_WritesNoMarkers(t *testing.T) {
	// Real local store: after a trails-off run, the trails.json file must not
	// exist, and TrailDensity must be empty.
	st := newRun(t, "off-no-markers")
	bus := events.NewBus(events.WithBuffer(16))
	sub := bus.Subscribe()
	evDone := collect(sub)

	ants := orderedAnts(&fakeFixer{}, "pkg/a.go", "pkg/b.go")
	if _, err := colony.Run(context.Background(), bus, colony.Options{
		Ants: ants, Store: st, RunID: "off-no-markers", Concurrency: 2, Trails: nil,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	bus.Close()
	<-evDone

	density, err := st.TrailDensity()
	if err != nil {
		t.Fatalf("TrailDensity: %v", err)
	}
	if len(density) != 0 {
		t.Errorf("trails off: density = %v, want empty (no markers written when off)", density)
	}
}

// TestTrailsOn_DensityBiasesScheduleOrder is the measurable-bias assertion: with
// trails ON and a seeded density, findings in the higher-density (species,
// location-class) class schedule EARLIER. Input order is alphabetical by file
// (the deterministic baseline); seeding pkg-c with the highest density and pkg-a
// with the lowest must invert the schedule to c, b, a — a measurable reordering
// that the trails-off baseline (a, b, c) does NOT produce.
func TestTrailsOn_DensityBiasesScheduleOrder(t *testing.T) {
	fixer := &fakeFixer{}
	// Deterministic input baseline (file-sorted, as the driver produces).
	files := []string{"pkg-a/x.go", "pkg-b/y.go", "pkg-c/z.go"}

	store := newFakeTrailStore()
	store.seed("demo", "pkg-c", 5) // highest density → schedule first
	store.seed("demo", "pkg-b", 2)
	store.seed("demo", "pkg-a", 0) // lowest → schedule last

	got := runOrderedAnts(t, "on-bias", orderedAnts(fixer, files...), store)
	want := []string{"pkg-c/z.go", "pkg-b/y.go", "pkg-a/x.go"}
	if !equalOrder(got, want) {
		t.Errorf("trails on: schedule order = %v, want density-biased %v", got, want)
	}
	if store.reads == 0 {
		t.Error("trails on: scheduler never read trail density")
	}

	// Control: the SAME findings with trails OFF keep the baseline order — proving
	// the reordering above is the density bias, not incidental.
	base := runOrderedAnts(t, "on-bias-control", orderedAnts(fixer, files...), nil)
	if !equalOrder(base, files) {
		t.Errorf("control (trails off): schedule order = %v, want baseline %v", base, files)
	}
}

// TestTrailsOn_EqualDensityKeepsStableOrder proves the bias only re-orders
// relative to a stable baseline: when every class has equal density (including
// the all-zero fresh-repo case), the schedule order is the unchanged input order
// — a stable sort, never a randomizing one.
func TestTrailsOn_EqualDensityKeepsStableOrder(t *testing.T) {
	fixer := &fakeFixer{}
	files := []string{"pkg-a/x.go", "pkg-b/y.go", "pkg-c/z.go"}

	store := newFakeTrailStore() // empty density → no bias
	got := runOrderedAnts(t, "on-equal", orderedAnts(fixer, files...), store)
	if !equalOrder(got, files) {
		t.Errorf("trails on, empty density: schedule order = %v, want stable input %v", got, files)
	}
}

// TestTrailsOn_VerifiedAntWritesMarker proves a verified-fixing ant writes a
// trail marker keyed by species + location class — and ONLY a verified one. Two
// findings in one class verify; the marker count for that class is 2.
func TestTrailsOn_VerifiedAntWritesMarker(t *testing.T) {
	store := newFakeTrailStore()
	ants := []colony.Ant{
		{Finding: findingAt("demo", "pkg/a.go", 1), Fixer: &fakeFixer{}, Verifier: fakePassVerifier{}},
		{Finding: findingAt("demo", "pkg/b.go", 2), Fixer: &fakeFixer{}, Verifier: fakePassVerifier{}},
	}
	st := newRun(t, "marker-write")
	bus := events.NewBus(events.WithBuffer(16))
	sub := bus.Subscribe()
	evDone := collect(sub)
	if _, err := colony.Run(context.Background(), bus, colony.Options{
		Ants: ants, Store: st, RunID: "marker-write", Concurrency: 2, Trails: store,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	bus.Close()
	<-evDone

	store.mu.Lock()
	got := store.recorded[engine.TrailKey{Species: "demo", LocationClass: "pkg"}]
	store.mu.Unlock()
	if got != 2 {
		t.Errorf("marker count for (demo, pkg) = %d, want 2 (one per verified fix)", got)
	}
}

// TestTrailsOn_SkippedAntWritesNoMarker proves a SKIPPED ant (failed verifier)
// writes NO trail marker — markers track verified fixes only (ADR-0003: written
// only after a verified fix).
func TestTrailsOn_SkippedAntWritesNoMarker(t *testing.T) {
	store := newFakeTrailStore()
	ants := []colony.Ant{
		{Finding: findingAt("demo", "pkg/ok.go", 1), Fixer: &fakeFixer{}, Verifier: fakePassVerifier{}},
		{Finding: findingAt("demo", "pkg/bad.go", 2), Fixer: &fakeFixer{}, Verifier: fakeFailVerifier{check: "compile"}},
	}
	st := newRun(t, "skip-no-marker")
	bus := events.NewBus(events.WithBuffer(16))
	sub := bus.Subscribe()
	evDone := collect(sub)
	if _, err := colony.Run(context.Background(), bus, colony.Options{
		Ants: ants, Store: st, RunID: "skip-no-marker", Concurrency: 2, Trails: store,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	bus.Close()
	<-evDone

	store.mu.Lock()
	total := 0
	for _, c := range store.recorded {
		total += c
	}
	store.mu.Unlock()
	if total != 1 {
		t.Errorf("total markers = %d, want 1 (only the verified ant writes a marker; the skipped one does not)", total)
	}
}

// TestTrailsOn_MarkerWriteFailureNeverGatesFix proves the OFF-THE-CRITICAL-PATH
// constraint (ADR-0003): even when every trail marker write FAILS, the verified
// fix is still staged and the run still reports it verified. A trail write is a
// scheduling hint, never a correctness gate.
func TestTrailsOn_MarkerWriteFailureNeverGatesFix(t *testing.T) {
	store := newFakeTrailStore()
	store.recordErr = fmt.Errorf("disk full") // every RecordTrail fails

	st := newRun(t, "marker-fail")
	bus := events.NewBus(events.WithBuffer(16))
	sub := bus.Subscribe()
	evDone := collect(sub)

	ants := orderedAnts(&fakeFixer{}, "pkg/a.go", "pkg/b.go")
	res, err := colony.Run(context.Background(), bus, colony.Options{
		Ants: ants, Store: st, RunID: "marker-fail", Concurrency: 2, Trails: store,
	})
	bus.Close()
	evs := <-evDone

	// The run must NOT fail because trail writes failed (off the critical path).
	if err != nil {
		t.Fatalf("Run returned error despite trail-write failures being off the critical path: %v", err)
	}
	if res.Verified != 2 || res.Staged != 2 {
		t.Errorf("result = %+v; want Verified 2, Staged 2 (trail-write failure must not unstage a verified fix)", res)
	}
	// Both diffs were actually staged in the Store.
	staged, err := st.ListStaged("marker-fail")
	if err != nil {
		t.Fatalf("ListStaged: %v", err)
	}
	if len(staged) != 2 {
		t.Errorf("staged = %d, want 2 (verified fixes staged despite trail-write failures)", len(staged))
	}
	// And both verified events were emitted.
	if c := countType(evs, events.TypeAntVerified); c != 2 {
		t.Errorf("ant.verified = %d, want 2", c)
	}
}

// TestTrailsOn_PersistsThroughLocalStore round-trips trail markers through the
// REAL local Store (not the fake), proving the persistence seam works end to
// end: a run with trails on writes markers, and a fresh Store reading the same
// base sees the accumulated density — the single-machine state that biases a
// LATER run (ADR-0003). Run under -race via the suite (the pool is concurrent).
func TestTrailsOn_PersistsThroughLocalStore(t *testing.T) {
	base := t.TempDir()
	st := local.New(base)
	if err := st.SaveRun(engine.Run{ID: "persist", StartedAt: "2026-05-24T00:00:00Z"}); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	bus := events.NewBus(events.WithBuffer(32))
	sub := bus.Subscribe()
	evDone := collect(sub)

	// Three findings: two in pkg-x, one in pkg-y — all verify.
	ants := []colony.Ant{
		{Finding: findingAt("demo", "pkg-x/a.go", 1), Fixer: &fakeFixer{}, Verifier: fakePassVerifier{}},
		{Finding: findingAt("demo", "pkg-x/b.go", 2), Fixer: &fakeFixer{}, Verifier: fakePassVerifier{}},
		{Finding: findingAt("demo", "pkg-y/c.go", 3), Fixer: &fakeFixer{}, Verifier: fakePassVerifier{}},
	}
	if _, err := colony.Run(context.Background(), bus, colony.Options{
		Ants: ants, Store: st, RunID: "persist", Concurrency: 4, Trails: st,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	bus.Close()
	<-evDone

	// A FRESH Store over the same base sees the persisted density (state survives
	// process restarts — it is the cross-run scheduling signal).
	fresh := local.New(base)
	density, err := fresh.TrailDensity()
	if err != nil {
		t.Fatalf("TrailDensity (fresh store): %v", err)
	}
	if got := density[engine.TrailKey{Species: "demo", LocationClass: "pkg-x"}]; got != 2 {
		t.Errorf("persisted density (demo, pkg-x) = %d, want 2", got)
	}
	if got := density[engine.TrailKey{Species: "demo", LocationClass: "pkg-y"}]; got != 1 {
		t.Errorf("persisted density (demo, pkg-y) = %d, want 1", got)
	}
}

// equalOrder reports whether two string slices are identical in length and order.
func equalOrder(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
