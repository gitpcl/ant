// Package colony — driver.go wires the full `ant fix` pipeline the CLI invokes:
// detect findings, resolve each finding's species to a Fixer + Verifier, build
// the colony Ants, run the loop, and render the live colony view (or the --json
// stream). It owns the event bus and the renderer goroutine so cmd/ant stays a
// thin caller with no concurrency/rendering machinery of its own — the boundary
// test forbids those constructs in the CLI layer (TECHSPEC §3).
//
// The driver does NOT reimplement the colony loop (loop.go) or the detector run
// (it reuses the same Detector interface scout uses). It is composition: detect
// → build Ants → Run → render. The optional fused apply (--apply) is delegated
// to an Applier the CLI injects, gated per-species by effective auto_apply
// (ADR-0002) so only trusted species auto-land.
package colony

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/events"
)

// Renderer selects how a fix run is rendered. Both consume the same event bus —
// one run rendered two ways (TECHSPEC §3, §11). The CLI picks TUI for an
// interactive TTY and JSON otherwise (colony-view.md §5).
type Renderer int

const (
	// RendererTUI attaches the live Bubble Tea colony view (colony-view.md).
	RendererTUI Renderer = iota
	// RendererJSON attaches the newline-delimited --json event stream.
	RendererJSON
)

// SpeciesRecipe is everything the driver needs to turn a finding owned by one
// species into a colony Ant: the Fixer that produces the diff, a per-finding
// Verifier builder (the species' ordered gate, diff-bounded first — built per
// finding because detector-clears is bound to the specific finding it must
// clear), and the effective auto_apply trust flag (ADR-0002) that decides
// whether --apply may auto-land its verified diffs. The CLI composition root
// builds these from the resolved species + config and hands them to the driver,
// so the driver depends only on the engine interfaces, never on the registry.
type SpeciesRecipe struct {
	Fixer       engine.Fixer
	NewVerifier func(engine.Finding) engine.Verifier
	AutoApply   bool
}

// Applier lands accepted/auto-applied staged diffs into the working tree. The
// driver calls it (when --apply is set) only for diffs from trusted species; the
// real implementation is go-git in-process (apply package), injected by the CLI
// so the driver has no git dependency.
type Applier interface {
	// ApplyRecords lands the given staged records and emits apply.done per landed
	// diff through the bus. It returns the number applied.
	ApplyRecords(ctx context.Context, bus *events.Bus, runID string, records []engine.StagedRecord) (int, error)
}

// SeenMarker records that a set of species participated in a completed run, so
// the NEXT run's freshly-installed trust override knows they were "present on a
// previous run" (TECHSPEC §6.3). The local store satisfies it (MarkSeen);
// keeping it a one-method interface (defined where used) keeps the driver free
// of the concrete store and lets tests inject a recorder. It is intentionally
// NOT on engine.Store — trust state is a local-store concern.
type SeenMarker interface {
	MarkSeen(names ...string) error
}

// DriveOptions parameterizes a `ant fix` run.
type DriveOptions struct {
	Scope     engine.Scope
	Detectors []engine.NamedDetector
	// Recipes maps a species name to its fix/verify/trust recipe. A finding whose
	// species has no recipe is skipped with a clear reason (never silently
	// dropped — PRD §6.3).
	Recipes     map[string]SpeciesRecipe
	Store       engine.Store
	RunID       string
	Concurrency int
	Now         func() time.Time

	// Apply, when set with ApplyFused, fuses apply for trusted species after the
	// colony stages. Nil Apply (or ApplyFused=false) leaves everything staged for
	// `ant review` — the default, nothing-applied behavior.
	Apply      Applier
	ApplyFused bool

	// SeenSpecies are the species names that participated in this run; after the
	// run completes the driver records them as "seen on a previous run" via
	// SeenMarker so the freshly-installed trust override (TECHSPEC §6.3) tracks
	// install state across runs. Empty/nil SeenMarker disables the recording (a
	// run with no trust persistence, e.g. unit tests with a bare engine.Store).
	SeenSpecies []string
	SeenMarker  SeenMarker

	// Rendering selection. Workers is the lane count for the TUI; Ascii/Color
	// toggle the glyph/ANSI fallbacks.
	Renderer Renderer
	Workers  int
	Ascii    bool
	Color    bool
}

// Drive runs the full fix pipeline and renders it. It returns the colony Result
// (verified/skipped/staged counts) and the first operational error (exit 2).
//
// Sequence (TECHSPEC §8): detect (parallel) → save the run → schedule ants
// through the pool (fix → verify → stage) → optionally fuse apply for trusted
// species → run.end. Every state change flows through the bus the renderer
// consumes. Nothing is applied unless ApplyFused is set AND the owning species'
// auto_apply is true (ADR-0002).
func Drive(ctx context.Context, w io.Writer, opts DriveOptions) (Result, error) {
	clock := opts.Now
	if clock == nil {
		clock = time.Now
	}
	runID := opts.RunID
	if runID == "" {
		runID = fmt.Sprintf("fix-%d", clock().UTC().UnixNano())
	}

	bus := events.NewBus()
	sub := bus.Subscribe()

	renderErr := make(chan error, 1)
	go func() {
		switch opts.Renderer {
		case RendererJSON:
			renderErr <- events.RenderJSON(w, sub)
		default:
			renderErr <- events.RenderTUI(ctx, w, sub, opts.Workers, opts.Ascii, opts.Color)
		}
	}()

	result, runErr := runFix(ctx, bus, runID, clock, opts)
	bus.Close()
	rErr := <-renderErr

	if runErr != nil {
		return result, runErr
	}
	return result, rErr
}

// runFix is the bus-publishing core (no rendering). It detects, persists the run
// so staging has a record to attach to, builds the Ants, runs the colony, and
// optionally fuses apply. Splitting it from Drive keeps the rendering/goroutine
// wiring separate from the orchestration and makes the core unit-testable with a
// captured bus (mirroring scout.Run vs scout.Drive).
func runFix(ctx context.Context, bus *events.Bus, runID string, clock func() time.Time, opts DriveOptions) (Result, error) {
	bus.Publish(events.Event{Type: events.TypeRunStart, RunStart: &events.RunStartPayload{RunID: runID, Scope: opts.Scope}})

	findings, err := detectFindings(ctx, opts.Detectors, opts.Scope, bus, runID)
	if err != nil {
		bus.Publish(events.Event{Type: events.TypeRunEnd, RunEnd: &events.RunEndPayload{
			RunID: runID, HighestSeverity: engine.SeverityUnknown.String(), Error: err.Error(),
		}})
		return Result{RunID: runID}, err
	}

	// Persist the run so staging (which requires an existing run) can attach
	// records to it. The findings ride along for provenance/audit.
	run := engine.Run{
		ID:        runID,
		StartedAt: clock().UTC().Format(time.RFC3339Nano),
		Scope:     opts.Scope,
		Findings:  findings,
	}
	if err := opts.Store.SaveRun(run); err != nil {
		opErr := fmt.Errorf("%w: save run %q: %v", engine.ErrOperational, runID, err)
		bus.Publish(events.Event{Type: events.TypeRunEnd, RunEnd: &events.RunEndPayload{
			RunID: runID, Findings: len(findings), HighestSeverity: highestOf(findings).String(), Error: opErr.Error(),
		}})
		return Result{RunID: runID}, opErr
	}

	ants := buildAnts(findings, opts.Recipes)

	// The driver owns the run framing (run.start above with the full scope, and
	// run.end below with the applied count), while the per-ant lifecycle in
	// loop.go (antRunner.process) owns the ant.start/ant.verified/ant.skipped
	// emissions. We therefore schedule through the pool directly rather than
	// calling colony.Run, which would publish a SECOND run.start/run.end and
	// break the single, well-formed event sequence the --json contract requires.
	return scheduleAndApply(ctx, bus, runID, opts, findings, ants)
}

// scheduleAndApply schedules the ants through the colony pool, fuses apply for
// trusted species when requested, and publishes the authoritative run.end. It
// reuses the per-ant lifecycle in loop.go (antRunner) so the fix/verify/stage +
// ant.* emission logic is not duplicated.
func scheduleAndApply(ctx context.Context, bus *events.Bus, runID string, opts DriveOptions, findings []engine.Finding, ants []Ant) (Result, error) {
	area := stageArea(opts.Store, runID)
	pool := newPool(opts.Concurrency)
	agg, runErr := pool.run(ctx, ants, &antRunner{runID: runID, scope: opts.Scope, bus: bus, area: area})

	result := Result{RunID: runID, Verified: agg.verified, Skipped: agg.skipped, Staged: agg.verified}

	applied := 0
	if runErr == nil && opts.ApplyFused && opts.Apply != nil {
		n, applyErr := fuseApply(ctx, bus, runID, opts)
		if applyErr != nil {
			runErr = applyErr
		}
		applied = n
	}

	// Record the species that participated in this run as "seen on a previous
	// run" so the NEXT run's freshly-installed override (TECHSPEC §6.3) tracks
	// install state correctly. This runs whenever the colony scheduled (runErr
	// nil), independent of --apply: a species is "seen" by having been present in
	// a completed run, not by landing a diff. A marking failure is non-fatal — it
	// only means the override stays conservative (the species is re-treated as
	// fresh next run), which is the safe direction; it never blocks the run.
	if runErr == nil && opts.SeenMarker != nil && len(opts.SeenSpecies) > 0 {
		_ = opts.SeenMarker.MarkSeen(opts.SeenSpecies...)
	}

	runEnd := &events.RunEndPayload{
		RunID:           runID,
		Findings:        len(findings),
		Verified:        agg.verified,
		Skipped:         agg.skipped,
		Applied:         applied,
		HighestSeverity: highestOf(findings).String(),
	}
	if runErr != nil {
		runEnd.Error = runErr.Error()
	}
	bus.Publish(events.Event{Type: events.TypeRunEnd, RunEnd: runEnd})
	return result, runErr
}

// fuseApply lands the staged diffs from trusted species (effective auto_apply
// true — ADR-0002). Untrusted species stay staged for `ant review`. It reads the
// full staged records, filters by the recipe's AutoApply flag (matched on the
// record's owning species), and asks the injected Applier to land exactly those.
func fuseApply(ctx context.Context, bus *events.Bus, runID string, opts DriveOptions) (int, error) {
	records, err := stageArea(opts.Store, runID).ListRecords()
	if err != nil {
		return 0, fmt.Errorf("%w: list staged for apply: %v", engine.ErrOperational, err)
	}
	trusted := make([]engine.StagedRecord, 0, len(records))
	for _, rec := range records {
		recipe, ok := opts.Recipes[rec.Finding.Species]
		if ok && recipe.AutoApply {
			trusted = append(trusted, rec)
		}
	}
	if len(trusted) == 0 {
		return 0, nil
	}
	return opts.Apply.ApplyRecords(ctx, bus, runID, trusted)
}

// buildAnts pairs each finding with its species' Fixer + Verifier from the
// recipe map. A finding whose species has no recipe gets a recipe-less Ant whose
// Fixer returns an error — the colony turns that into a clean ant.skipped with a
// reason, so an unconfigured species surfaces as a visible skip rather than a
// silent drop (PRD §6.3). Findings are sorted deterministically first so ant ids
// are stable across runs (golden/TUI lanes).
func buildAnts(findings []engine.Finding, recipes map[string]SpeciesRecipe) []Ant {
	sorted := make([]engine.Finding, len(findings))
	copy(sorted, findings)
	sortFindings(sorted)

	ants := make([]Ant, 0, len(sorted))
	for _, f := range sorted {
		recipe, ok := recipes[f.Species]
		if !ok || recipe.Fixer == nil {
			ants = append(ants, Ant{Finding: f, Fixer: missingRecipeFixer{species: f.Species}, Verifier: passThroughVerifier{}})
			continue
		}
		verifier := engine.Verifier(passThroughVerifier{})
		if recipe.NewVerifier != nil {
			verifier = recipe.NewVerifier(f)
		}
		ants = append(ants, Ant{Finding: f, Fixer: recipe.Fixer, Verifier: verifier})
	}
	return ants
}

// missingRecipeFixer is the Fixer used for a finding whose species has no recipe:
// it always fails, so the colony emits ant.skipped with a clear reason. This
// makes a misconfiguration visible instead of dropping the finding silently.
type missingRecipeFixer struct{ species string }

func (f missingRecipeFixer) Fix(context.Context, engine.FixTask) (engine.ProposedDiff, error) {
	return engine.ProposedDiff{}, fmt.Errorf("no fix recipe configured for species %q (species not enabled or not built)", f.species)
}

// passThroughVerifier is never reached for a missingRecipeFixer (the fix fails
// first), but the colony requires a non-nil Verifier; it passes trivially.
type passThroughVerifier struct{}

func (passThroughVerifier) Verify(context.Context, engine.ProposedDiff, engine.Scope) engine.VerifyResult {
	return engine.VerifyResult{Passed: true}
}

// highestOf returns the highest severity across findings for the run.end
// summary / CI gate.
func highestOf(findings []engine.Finding) engine.Severity {
	highest := engine.SeverityUnknown
	for _, f := range findings {
		if f.Severity > highest {
			highest = f.Severity
		}
	}
	return highest
}

// sortFindings orders findings deterministically (species, file, line, col) so
// ant ids are stable across runs — mirrors scout.sortFindings.
func sortFindings(findings []engine.Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Species != b.Species {
			return a.Species < b.Species
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Span.StartLine != b.Span.StartLine {
			return a.Span.StartLine < b.Span.StartLine
		}
		return a.Span.StartCol < b.Span.StartCol
	})
}
