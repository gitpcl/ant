package scout

import (
	"context"
	"io"

	"github.com/gitpcl/ant/internal/engine/events"
)

// OutputFormat selects how a run is rendered. Both formats consume the same
// event bus — they are one run rendered two ways (TECHSPEC §3, §11).
type OutputFormat int

const (
	// FormatHuman is the default plain-text rendering.
	FormatHuman OutputFormat = iota
	// FormatJSON emits the newline-delimited --json event stream.
	FormatJSON
)

// RenderOptions carries the human-renderer toggles the CLI flags select. Detail
// adds the per-finding code snippet; All lists every finding (the full flat
// list) instead of the default severity-led digest. They compose: --all --detail
// is the full flat list with snippets. Neither affects --json (the byte contract
// is rendered by RenderJSON, untouched).
type RenderOptions struct {
	Detail bool
	All    bool
}

// Drive is the scout entry point the CLI calls: it owns the event bus, the
// renderer goroutine, and the scout run, so cmd/ant stays a pure caller with no
// concurrency or rendering machinery of its own (the boundary test forbids
// goroutines, channels, and encoding/json in the CLI layer). It wires a single
// subscriber (the chosen renderer) to the bus, runs scout, closes the bus, and
// waits for the renderer to finish draining.
//
// Returns the Result (for the caller's exit-code decision) and the first error
// from either scouting (operational, exit 2) or rendering.
func Drive(ctx context.Context, w io.Writer, format OutputFormat, render RenderOptions, opts Options) (Result, error) {
	bus := events.NewBus()
	sub := bus.Subscribe()

	renderErr := make(chan error, 1)
	go func() {
		switch format {
		case FormatJSON:
			renderErr <- events.RenderJSON(w, sub)
		default:
			renderErr <- events.RenderHuman(w, sub, events.HumanOptions{Detail: render.Detail, All: render.All})
		}
	}()

	result, scoutErr := Run(ctx, bus, opts)
	bus.Close() // unblocks the renderer's range over the subscription
	rErr := <-renderErr

	if scoutErr != nil {
		return result, scoutErr
	}
	return result, rErr
}
