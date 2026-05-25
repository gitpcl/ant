package telemetry

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/events"
)

// recordingTransport is the injected test fake: it captures every Report sent so
// a test can assert exactly how many sends happened and what they carried. No
// network, no live endpoint.
type recordingTransport struct {
	reports []Report
}

func (r *recordingTransport) Send(rep Report) error {
	r.reports = append(r.reports, rep)
	return nil
}

// fixedDate is a deterministic coarse clock for stable assertions.
func fixedDate() string { return "2026-05-24" }

// publishFullStream drives a representative run through the bus: a couple of
// findings (species usage), one verified fix, and one skipped fix whose verifier
// caught it (a verifier catch). Every event type the sink folds is exercised.
func publishFullStream(bus *events.Bus) {
	bus.Publish(events.Event{Type: events.TypeRunStart, RunStart: &events.RunStartPayload{RunID: "r1"}})
	bus.Publish(events.Event{Type: events.TypeDetectFinding, DetectFinding: &events.DetectFindingPayload{
		RunID: "r1", Finding: engine.Finding{Species: "unused-import", File: "/secret/path/main.go", Snippet: "import \"os\""},
	}})
	bus.Publish(events.Event{Type: events.TypeDetectFinding, DetectFinding: &events.DetectFindingPayload{
		RunID: "r1", Finding: engine.Finding{Species: "unused-import", File: "/secret/path/util.go"},
	}})
	bus.Publish(events.Event{Type: events.TypeDetectFinding, DetectFinding: &events.DetectFindingPayload{
		RunID: "r1", Finding: engine.Finding{Species: "n+1-query", File: "/secret/path/db.go"},
	}})
	bus.Publish(events.Event{Type: events.TypeAntVerified, AntVerified: &events.AntVerifiedPayload{
		RunID: "r1", AntID: 1,
		Diff: engine.ProposedDiff{Files: []engine.FileDiff{{Path: "/secret/path/main.go", Patch: "secret diff body"}}},
	}})
	bus.Publish(events.Event{Type: events.TypeAntSkipped, AntSkipped: &events.AntSkippedPayload{
		RunID: "r1", AntID: 2,
		Finding:     engine.Finding{Species: "n+1-query", File: "/secret/path/db.go"},
		FailedCheck: engine.CheckResult{Name: "compile", Passed: false, Detail: "secret build error"},
		Reason:      "compile failed",
	}})
	bus.Publish(events.Event{Type: events.TypeRunEnd, RunEnd: &events.RunEndPayload{RunID: "r1"}})
}

// TestDisabledCollectsAndSendsNothing is the VALIDATE-FIRST test and the core
// privacy guarantee: with telemetry OFF (the default), New returns a nil/no-op
// sink that subscribes to NOTHING, collects NOTHING, and sends NOTHING — even
// after a full event stream and review decisions. This is collection
// short-circuited, not transmission withheld.
func TestDisabledCollectsAndSendsNothing(t *testing.T) {
	tr := &recordingTransport{}
	sink := New(false, tr, fixedDate) // enabled=false → the default posture

	if sink.Enabled() {
		t.Fatal("a disabled sink must report Enabled()==false")
	}

	bus := events.NewBus()
	sink.Observe(bus) // must be a no-op: no subscription
	publishFullStream(bus)
	bus.Close()

	// Review decisions on a disabled sink must also collect nothing.
	sink.RecordReviewDecision(engine.MarkAccepted)
	sink.RecordReviewDecision(engine.MarkSkipped)

	if err := sink.Close(); err != nil {
		t.Fatalf("Close on a disabled sink must be a no-op: %v", err)
	}

	if len(tr.reports) != 0 {
		t.Fatalf("disabled telemetry must SEND nothing; transport received %d report(s)", len(tr.reports))
	}

	// And it must have collected nothing: the report snapshot is the zero value.
	rep := sink.Report()
	if !reflect.DeepEqual(rep, Report{}) {
		t.Fatalf("disabled telemetry must COLLECT nothing; got non-zero report: %+v", rep)
	}

	// Defense in depth: the bus must have had no subscriber, so publishing did
	// not block and nothing was observed. A fresh sink confirms no global state.
	if New(false, tr, fixedDate) != nil {
		t.Fatal("New(false,...) must return a nil sink (the disabled state)")
	}
}

// TestDisabledNeverSubscribes proves the guard short-circuits COLLECTION: an
// off sink registers no subscriber on the bus, so there is no in-memory profile
// to leak. We assert by publishing to a bus the off sink "observed" and checking
// the bus delivered to zero telemetry subscribers (a real subscriber would
// receive the events; the off sink has none).
func TestDisabledNeverSubscribes(t *testing.T) {
	sink := New(false, &recordingTransport{}, fixedDate)
	bus := events.NewBus()

	// A control subscriber to prove the bus is live and delivering.
	control := bus.Subscribe()
	got := make(chan int, 1)
	go func() {
		n := 0
		for range control.C {
			n++
		}
		got <- n
	}()

	sink.Observe(bus) // off → no subscription added
	bus.Publish(events.Event{Type: events.TypeDetectFinding, DetectFinding: &events.DetectFindingPayload{
		RunID: "r1", Finding: engine.Finding{Species: "unused-import"},
	}})
	bus.Close()

	if n := <-got; n != 1 {
		t.Fatalf("control subscriber should have received 1 event, got %d", n)
	}
	// If the off sink had subscribed, Publish would have fanned out to it too;
	// the sink folded nothing (its report is zero), confirming no subscription.
	if rep := sink.Report(); !reflect.DeepEqual(rep, Report{}) {
		t.Fatalf("off sink must not have folded any event; got %+v", rep)
	}
}

// TestEnabledEmitsOnlyAggregates proves the enabled path produces the right
// privacy-safe aggregates and sends exactly one Report. It feeds the full
// stream + review decisions and asserts species usage, accept rate, and the
// verifier catch rate (the PRD §8 metric) are captured correctly.
func TestEnabledEmitsOnlyAggregates(t *testing.T) {
	tr := &recordingTransport{}
	sink := New(true, tr, fixedDate)
	if !sink.Enabled() {
		t.Fatal("an enabled sink must report Enabled()==true")
	}

	bus := events.NewBus()
	sink.Observe(bus)
	publishFullStream(bus)
	bus.Close()

	// Two review accepts, one skip → accept rate 2/3.
	sink.RecordReviewDecision(engine.MarkAccepted)
	sink.RecordReviewDecision(engine.MarkAccepted)
	sink.RecordReviewDecision(engine.MarkSkipped)

	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(tr.reports) != 1 {
		t.Fatalf("enabled telemetry must send exactly one report on Close; got %d", len(tr.reports))
	}
	rep := tr.reports[0]

	// Species usage: which public species ran + counts.
	if rep.SpeciesUsage["unused-import"] != 2 {
		t.Errorf("unused-import usage = %d, want 2", rep.SpeciesUsage["unused-import"])
	}
	if rep.SpeciesUsage["n+1-query"] != 1 {
		t.Errorf("n+1-query usage = %d, want 1", rep.SpeciesUsage["n+1-query"])
	}

	// Verifier catch rate: 1 caught of 2 proposed (1 verified + 1 skipped) = 0.5.
	if rep.VerifierCatches != 1 {
		t.Errorf("verifier catches = %d, want 1", rep.VerifierCatches)
	}
	if rep.FixesProposed != 2 {
		t.Errorf("fixes proposed = %d, want 2", rep.FixesProposed)
	}
	if rep.VerifierCatchRate != 0.5 {
		t.Errorf("verifier catch rate = %v, want 0.5", rep.VerifierCatchRate)
	}

	// Accept rate: 2 of 3.
	if rep.ReviewDecisions != 3 {
		t.Errorf("review decisions = %d, want 3", rep.ReviewDecisions)
	}
	if got := rep.AcceptRate; got < 0.66 || got > 0.67 {
		t.Errorf("accept rate = %v, want ~0.667", got)
	}

	// Coarse, public metadata only.
	if rep.AntVersion != engine.Version {
		t.Errorf("ant version = %q, want %q", rep.AntVersion, engine.Version)
	}
	if rep.Date != fixedDate() {
		t.Errorf("date = %q, want %q (coarse, date-only)", rep.Date, fixedDate())
	}
}

// TestCloseSendsAtMostOnce proves Close is idempotent: a telemetry failure path
// or a double Close never double-sends.
func TestCloseSendsAtMostOnce(t *testing.T) {
	tr := &recordingTransport{}
	sink := New(true, tr, fixedDate)
	_ = sink.Close()
	_ = sink.Close()
	if len(tr.reports) != 1 {
		t.Fatalf("Close must send at most once; got %d sends", len(tr.reports))
	}
}

// codeOrPIIWords are substrings that, if they appeared in a Report field NAME,
// would signal the payload can carry source code, diffs, paths, or PII. The
// Report is aggregates-only by design; this guards that no such field is ever
// added.
var codeOrPIIWords = []string{
	"file", "path", "dir", "snippet", "code", "diff", "patch", "source",
	"content", "message", "rationale", "finding", "detail", "repo", "user",
	"email", "name", "author", "token", "secret", "url", "host", "ip",
}

// TestReportShapeHasNoCodeOrPII reflectively walks the Report struct and asserts
// EVERY field is a privacy-safe aggregate: a scalar number, a rate, the version,
// the coarse date, or a species-name→count map (map[string]int). It fails if any
// field could carry code, a diff, a file path, a message, or PII — including a
// nested struct or a string slice that could smuggle content. This is the
// payload-privacy contract test.
func TestReportShapeHasNoCodeOrPII(t *testing.T) {
	rt := reflect.TypeOf(Report{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		assertPrivacySafeField(t, f.Name, f.Type)

		// A field NAME hinting at code/PII is itself a red flag (e.g. a future
		// "FilePath" or "SampleSnippet"). The one allowed exception is the
		// species-usage map, whose keys are PUBLIC species names — but that field
		// is literally named "SpeciesUsage" and species names are public IDs.
		lower := strings.ToLower(f.Name)
		for _, w := range codeOrPIIWords {
			if strings.Contains(lower, w) {
				// AntVersion contains "version" not in the list; SpeciesUsage is the
				// sanctioned public-name map. Anything else is a leak.
				if f.Name == "SpeciesUsage" {
					continue
				}
				t.Errorf("Report field %q contains code/PII-suggesting token %q — telemetry must carry aggregates only", f.Name, w)
			}
		}
	}
}

// assertPrivacySafeField fails unless typ is an allowed aggregate-carrying type:
// a number, a float rate, a string limited to version/date metadata, or
// map[string]int for species counts. Structs, byte slices, []string, and
// arbitrary maps are rejected because they could carry code/paths/PII.
func assertPrivacySafeField(t *testing.T, name string, typ reflect.Type) {
	t.Helper()
	switch typ.Kind() {
	case reflect.Int, reflect.Int64, reflect.Float64:
		return // a counter or a rate — safe
	case reflect.String:
		// Only the two sanctioned metadata strings are permitted.
		if name == "AntVersion" || name == "Date" {
			return
		}
		t.Errorf("Report field %q is a free string — only AntVersion/Date metadata strings are allowed (no code/paths)", name)
	case reflect.Map:
		if typ.Key().Kind() == reflect.String && typ.Elem().Kind() == reflect.Int {
			return // map[string]int — species NAME → count, names are public IDs
		}
		t.Errorf("Report field %q is map[%s]%s — only map[string]int (species name → count) is allowed", name, typ.Key(), typ.Elem())
	default:
		t.Errorf("Report field %q has disallowed kind %s — only numeric counters, rates, version/date strings, and map[string]int are privacy-safe", name, typ.Kind())
	}
}

// TestFixerErrorSkipIsNotAVerifierCatch proves the catch rate is honest: an
// ant.skipped caused by a fixer error or a missing recipe (FailedCheck.Name ==
// "fix" — no diff was ever proposed) is NOT counted as a verifier catch, and
// does not inflate the proposed-fixes denominator. Only a real proposed fix
// stopped by a verifier counts (PRD §8).
func TestFixerErrorSkipIsNotAVerifierCatch(t *testing.T) {
	tr := &recordingTransport{}
	sink := New(true, tr, fixedDate)

	bus := events.NewBus()
	sink.Observe(bus)
	// One real verifier catch (compile failed on a proposed diff).
	bus.Publish(events.Event{Type: events.TypeAntSkipped, AntSkipped: &events.AntSkippedPayload{
		FailedCheck: engine.CheckResult{Name: "compile", Passed: false},
	}})
	// One fixer-error skip (no diff proposed) — must NOT be a catch.
	bus.Publish(events.Event{Type: events.TypeAntSkipped, AntSkipped: &events.AntSkippedPayload{
		FailedCheck: engine.CheckResult{Name: "fix", Passed: false, Detail: "no diff"},
	}})
	// One missing-recipe skip (also "fix") — must NOT be a catch.
	bus.Publish(events.Event{Type: events.TypeAntSkipped, AntSkipped: &events.AntSkippedPayload{
		FailedCheck: engine.CheckResult{Name: "fix", Passed: false, Detail: "no recipe"},
	}})
	bus.Close()
	_ = sink.Close()

	rep := tr.reports[0]
	if rep.VerifierCatches != 1 {
		t.Errorf("verifier catches = %d, want 1 (only the compile catch counts; 'fix' skips do not)", rep.VerifierCatches)
	}
	if rep.FixesProposed != 1 {
		t.Errorf("fixes proposed = %d, want 1 (a 'fix' skip means no fix was proposed)", rep.FixesProposed)
	}
	if rep.VerifierCatchRate != 1.0 {
		t.Errorf("verifier catch rate = %v, want 1.0 (1 catch of 1 proposed)", rep.VerifierCatchRate)
	}
}
