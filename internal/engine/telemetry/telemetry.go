// Package telemetry is the opt-in, privacy-respecting metrics sink (PRD §8,
// TECHSPEC §11): the instrumentation that lets the OSS phase discover the
// enterprise product. It is OFF BY DEFAULT and the privacy posture is the
// contract, not a footnote:
//
//   - Off by default. A Sink is created only behind an explicit enabled flag
//     ([telemetry] enabled = true in ant.toml). With no config / the default
//     config, New returns nil — a no-op Sink that subscribes to nothing,
//     collects nothing, and sends nothing. The guard short-circuits COLLECTION,
//     not merely transmission: there is no in-memory profile to leak when off.
//   - Aggregates only. The emitted Report carries privacy-safe counters —
//     species usage (which public species ran + counts), the review accept rate,
//     and the verifier catch rate (the share of proposed fixes their own
//     verifier stopped before reaching the user — PRD §8, "proves the gate
//     works"). Species NAMES are public identifiers and are fine; a file path,
//     a code snippet, a diff, or any repo identifier is NOT, and no Report field
//     can carry one.
//   - Decoupled. The Sink is a plain consumer of the event bus (the same
//     Subscribe() seam the TUI and --json renderers use). It never reaches into
//     colony internals, and it does NOT touch the frozen --json event contract
//     (TECHSPEC §12) — telemetry is a separate consumer.
//
// The transport is injectable (a Transport interface) so tests use a recording
// fake and v1 ships a no-op: ant does NOT phone home to a live server by default
// or in tests, and adds no live-network dependency.
package telemetry

import (
	"sync"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/events"
)

// Transport delivers a finished Report. It is the single, injectable seam
// between the aggregate sink and the outside world: tests pass a recording fake,
// and v1 ships NopTransport (no network). A real HTTP transport can be added
// later without changing the sink or the privacy posture — the Report it
// receives is already aggregates-only by construction.
type Transport interface {
	Send(Report) error
}

// checkNameFix is the CheckResult name the colony uses for a skip that is NOT a
// verifier catch: a fixer that produced no diff, or a finding with no recipe
// (colony/loop.go). It must match the engine's value so the catch-rate metric
// excludes "no fix was ever proposed" from the count of fixes the gate caught.
const checkNameFix = "fix"

// NopTransport is the default transport: it accepts a Report and does nothing
// with it. v1 collects aggregates (only when explicitly enabled) but does not
// transmit them anywhere — there is no live endpoint and no network dependency.
// It exists so the enabled path is exercisable end-to-end without phoning home.
type NopTransport struct{}

// Send discards the report. Always succeeds.
func (NopTransport) Send(Report) error { return nil }

// Sink folds bus events into privacy-safe aggregates and, on Close, emits a
// single Report through its Transport. A nil *Sink is the disabled state: every
// method is a safe no-op, so callers wire telemetry unconditionally and the
// enabled flag alone decides whether anything happens. This is the guard: when
// telemetry is off, New returns nil and there is no subscriber, no fold, and no
// Report — provably zero collection.
type Sink struct {
	mu    sync.Mutex
	agg   aggregate
	tr    Transport
	now   func() string // injectable coarse (date-only) clock for stable tests
	subs  []*events.Subscription
	wg    sync.WaitGroup // tracks in-flight consume goroutines
	flush sync.Once
}

// New constructs a telemetry Sink ONLY when enabled is true. When enabled is
// false (the default — bare `ant` with no [telemetry] section), it returns nil:
// the disabled, no-op Sink. The guard lives here so collection is impossible
// when off — there is nothing to subscribe, nothing to fold, nothing to leak.
//
// transport is injectable; pass a recording fake in tests. A nil transport
// degrades to NopTransport so an enabled-but-unconfigured sink still never
// phones home. now supplies the coarse timestamp (date-only); a nil now uses the
// built-in UTC date so the Report carries no fine-grained, fingerprintable time.
func New(enabled bool, transport Transport, now func() string) *Sink {
	if !enabled {
		return nil // off by default: no sink, no collection, no send
	}
	if transport == nil {
		transport = NopTransport{}
	}
	if now == nil {
		now = utcDate
	}
	return &Sink{
		agg: newAggregate(),
		tr:  transport,
		now: now,
	}
}

// Enabled reports whether collection is active. A nil (disabled) Sink is never
// enabled — the one place the on/off state is observed, kept nil-safe so callers
// need no separate guard.
func (s *Sink) Enabled() bool { return s != nil }

// Observe attaches the sink to a bus as a plain subscriber and folds events into
// the running aggregate in a background goroutine until the bus closes or Close
// is called. It is a no-op on a nil (disabled) sink: nothing subscribes, so
// nothing is collected. Observe may be called for more than one bus in a process
// (e.g. a fix run then a review pass); each subscription folds into the same
// aggregate.
func (s *Sink) Observe(bus *events.Bus) {
	if s == nil || bus == nil {
		return
	}
	sub := bus.Subscribe()
	s.mu.Lock()
	s.subs = append(s.subs, sub)
	s.wg.Add(1)
	s.mu.Unlock()
	go s.consume(sub)
}

// consume drains one subscription, folding each event into the aggregate. It
// returns when the bus closes the channel (or Close unsubscribes it).
func (s *Sink) consume(sub *events.Subscription) {
	defer s.wg.Done()
	for ev := range sub.C {
		s.fold(ev)
	}
}

// RecordReviewDecision folds a single `ant review` accept/skip mark into the
// accept-rate aggregate. Review marks are not bus events (they are persisted via
// the Store), so the review front door reports them here directly. It is a
// no-op on a nil sink. Only the decision (accepted vs not) is recorded — never
// the diff, the file, or any content.
func (s *Sink) RecordReviewDecision(mark engine.Mark) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agg.reviewTotal++
	if mark == engine.MarkAccepted {
		s.agg.reviewAccepted++
	}
}

// fold updates the aggregate from one bus event. Only privacy-safe scalars are
// read: the species NAME (a public identifier) from a finding, and the pass/fail
// shape of a verifier check. No file path, snippet, diff, or message is ever
// retained.
func (s *Sink) fold(ev events.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch ev.Type {
	case events.TypeDetectFinding:
		if ev.DetectFinding != nil {
			// Species usage: which species ran + how often. The name only.
			s.agg.speciesUsage[ev.DetectFinding.Finding.Species]++
		}
	case events.TypeAntVerified:
		// A fix that was proposed, passed its own verifier gate, and reached
		// staging. It is a proposed fix that the gate let through.
		s.agg.fixVerified++
	case events.TypeAntSkipped:
		// A skip is a VERIFIER CATCH only when a fix was actually PROPOSED and then
		// stopped by a verifier (PRD §8 — "the share of proposed fixes stopped by
		// their own verifier before reaching the user"). The colony emits the same
		// ant.skipped event for two other cases that are NOT verifier catches: a
		// fixer that produced no diff, and a finding with no configured recipe —
		// both carry a failing check named "fix" (loop.go), meaning no fix was ever
		// proposed. Excluding "fix" keeps the catch rate honest: it counts only the
		// gate stopping a real proposed fix, never a fix that never existed.
		if ev.AntSkipped != nil && ev.AntSkipped.FailedCheck.Name != checkNameFix {
			s.agg.verifierCatches++
		}
	}
}

// Close detaches every subscription, builds the final aggregate Report, and
// sends it once through the Transport. It is a no-op (returning nil) on a nil
// (disabled) sink. Close is idempotent: the Report is sent at most once even if
// called repeatedly. The returned error is the transport's, surfaced so a caller
// that cares can log it — but a telemetry failure must never break a run, so the
// CLI ignores it.
func (s *Sink) Close() error {
	if s == nil {
		return nil
	}
	// Detach every subscription (closes each channel, ending its consume loop),
	// then wait for the in-flight consumers to finish folding so the Report is a
	// complete snapshot — not a race against a still-draining goroutine.
	s.mu.Lock()
	subs := s.subs
	s.subs = nil
	s.mu.Unlock()
	for _, sub := range subs {
		sub.Unsubscribe()
	}
	s.wg.Wait()

	var err error
	s.flush.Do(func() {
		err = s.tr.Send(s.Report())
	})
	return err
}

// Report snapshots the current aggregate as the privacy-safe payload. It is
// exported so tests can assert the shape directly; Close also calls it. On a nil
// sink it returns the zero Report. The returned Report is a value with copied
// maps, so the caller cannot mutate the sink's state.
func (s *Sink) Report() Report {
	if s == nil {
		return Report{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agg.report(s.now())
}
