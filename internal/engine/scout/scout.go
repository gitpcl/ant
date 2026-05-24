// Package scout runs the read-only detection pass: resolve a scope, run the
// enabled species' detectors in parallel, and report findings through the event
// bus. It mutates nothing in the working tree (TECHSPEC §7). It lives in its own
// package — not in engine — so the engine package stays pure types+interfaces
// and avoids an import cycle with engine/events (events already imports engine
// for its payload types).
package scout

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/events"
)

// Options parameterizes a scout run. Detectors are injected (composition happens
// at the CLI/engine boundary) so scout depends only on the Detector interface —
// the colony's run loop in a later sprint reuses the same detector set.
// SeverityFilter, when set, drops findings below it; AntFilter, when non-empty,
// limits the run to the named species.
type Options struct {
	Scope          engine.Scope
	Detectors      []engine.NamedDetector
	SeverityFilter engine.Severity // SeverityUnknown means "no filter"
	AntFilter      []string        // empty means all enabled species
	RunID          string          // optional; generated from the clock if empty
	Now            func() time.Time
}

// Result is the outcome of a scout run: the findings (sorted for stable
// rendering and golden tests) and the highest severity seen, which the CI gate
// compares against --fail-on. Scout mutates nothing — this is a read-only
// observation (TECHSPEC §7).
type Result struct {
	RunID           string
	Findings        []engine.Finding
	HighestSeverity engine.Severity
}

// Run executes detectors in parallel over the scope, publishes the canonical
// event sequence (run.start → detect.finding* → run.end) to the bus, and
// returns the collected findings. It never writes to the working tree.
//
// The bus is the single source of truth: the human renderer and the --json
// renderer both consume the events this emits, so the two outputs are the same
// run rendered two ways (TECHSPEC §3, §11). A detector returning an operational
// error (e.g. a missing ast-grep binary) aborts the run and the error is
// returned for exit-code classification (TECHSPEC §7.1).
func Run(ctx context.Context, bus *events.Bus, opts Options) (Result, error) {
	clock := opts.Now
	if clock == nil {
		clock = time.Now
	}
	runID := opts.RunID
	if runID == "" {
		runID = fmt.Sprintf("scout-%d", clock().UTC().UnixNano())
	}

	detectors := filterDetectors(opts.Detectors, opts.AntFilter)

	bus.Publish(events.Event{
		Type:     events.TypeRunStart,
		RunStart: &events.RunStartPayload{RunID: runID, Scope: opts.Scope},
	})

	findings, err := runDetectors(ctx, detectors, opts.Scope)
	if err != nil {
		// Still close the run so the stream is well-formed (run.start → run.end)
		// and consumers ranging it terminate. The Error field marks this as an
		// aborted run so renderers do not report a misleading clean summary, and
		// front doors parsing --json see the failure.
		bus.Publish(events.Event{
			Type: events.TypeRunEnd,
			RunEnd: &events.RunEndPayload{
				RunID:           runID,
				HighestSeverity: engine.SeverityUnknown.String(),
				Error:           err.Error(),
			},
		})
		return Result{RunID: runID}, err
	}

	findings = applySeverityFilter(findings, opts.SeverityFilter)
	sortFindings(findings)

	highest := engine.SeverityUnknown
	for _, f := range findings {
		if f.Severity > highest {
			highest = f.Severity
		}
		bus.Publish(events.Event{
			Type:          events.TypeDetectFinding,
			DetectFinding: &events.DetectFindingPayload{RunID: runID, Finding: f},
		})
	}

	bus.Publish(events.Event{
		Type: events.TypeRunEnd,
		RunEnd: &events.RunEndPayload{
			RunID:           runID,
			Findings:        len(findings),
			HighestSeverity: highest.String(),
		},
	})

	return Result{RunID: runID, Findings: findings, HighestSeverity: highest}, nil
}

// filterDetectors keeps only detectors whose species is in the filter. An empty
// filter keeps all of them. It returns a new slice (no mutation of the input).
func filterDetectors(all []engine.NamedDetector, filter []string) []engine.NamedDetector {
	if len(filter) == 0 {
		out := make([]engine.NamedDetector, len(all))
		copy(out, all)
		return out
	}
	allow := make(map[string]struct{}, len(filter))
	for _, name := range filter {
		allow[name] = struct{}{}
	}
	out := make([]engine.NamedDetector, 0, len(all))
	for _, d := range all {
		if _, ok := allow[d.Species]; ok {
			out = append(out, d)
		}
	}
	return out
}

// runDetectors fans the detectors out across goroutines, each scanning the same
// scope, and gathers their findings. The first operational error aborts via
// context cancellation; detection itself mutates nothing. Findings are merged
// into a single slice (order is normalized later by sortFindings).
func runDetectors(ctx context.Context, detectors []engine.NamedDetector, scope engine.Scope) ([]engine.Finding, error) {
	if len(detectors) == 0 {
		return []engine.Finding{}, nil
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu       sync.Mutex
		all      []engine.Finding
		firstErr error
		wg       sync.WaitGroup
	)

	for _, nd := range detectors {
		wg.Add(1)
		go func(nd engine.NamedDetector) {
			defer wg.Done()
			found, err := nd.Detector.Detect(runCtx, scope)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
					cancel() // stop peers early on the first failure
				}
				return
			}
			all = append(all, found...)
		}(nd)
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	return all, nil
}

// applySeverityFilter returns findings at or above floor. SeverityUnknown means
// "no filter" and returns all findings. The result is a new slice.
func applySeverityFilter(findings []engine.Finding, floor engine.Severity) []engine.Finding {
	if floor == engine.SeverityUnknown {
		out := make([]engine.Finding, len(findings))
		copy(out, findings)
		return out
	}
	out := make([]engine.Finding, 0, len(findings))
	for _, f := range findings {
		if f.Severity.AtLeast(floor) {
			out = append(out, f)
		}
	}
	return out
}

// sortFindings orders findings deterministically (species, file, line, column)
// so human output, --json output, and golden tests are stable regardless of the
// nondeterministic order detector goroutines complete in.
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
