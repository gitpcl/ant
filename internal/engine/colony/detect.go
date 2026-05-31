package colony

import (
	"context"
	"sync"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/events"
	"github.com/gitpcl/ant/internal/engine/stage"
)

// detectFindings runs the colony's detection pass: every species' detector over
// the scope in parallel, publishing detect.finding per result so the live view's
// queue counter and the --json stream see findings as they arrive (colony-view.md
// §0). It mirrors scout's detector fan-out (it is the same Detector interface and
// the same TECHSPEC §8 step 2) but emits through the fix run's bus rather than
// owning its own run framing. The first operational error (e.g. missing ast-grep)
// aborts via context cancellation and is returned for exit-code classification.
func detectFindings(ctx context.Context, detectors []engine.NamedDetector, scope engine.Scope, bus *events.Bus, runID string) ([]engine.Finding, error) {
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
					cancel()
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

	// Honor [ignore].paths the SAME way scout does: drop findings under an ignored
	// path before they are published, counted, or turned into fix tasks. The
	// colony builds fix tasks from these findings (loop.go), so filtering here is
	// the single boundary that gives "no findings AND no fix tasks" for free — and
	// every detector inherits it without a per-detector check (TECHSPEC §9).
	all = engine.FilterIgnored(all, scope.IgnoreGlobs)

	// Publish findings in a deterministic order so the queue counter and --json
	// stream are stable regardless of detector goroutine completion order.
	sortFindings(all)
	for _, f := range all {
		bus.Publish(events.Event{Type: events.TypeDetectFinding, DetectFinding: &events.DetectFindingPayload{RunID: runID, Finding: f}})
	}
	return all, nil
}

// stageArea is a tiny constructor wrapper so the driver reads naturally
// (stage.New is the underlying call). It keeps the driver's import of the stage
// package localized to one spot.
func stageArea(store engine.Store, runID string) *stage.Area {
	return stage.New(store, runID)
}
