package events

import (
	"encoding/json"
	"fmt"
	"io"
)

// RenderJSON drains a subscription and writes one JSON object per event to w,
// newline-delimited (JSON Lines), from run.start through run.end. It is the
// --json front-door contract: the front doors (Claude Code skill, Pi extension,
// CI) parse exactly this stream, so the per-event shape is frozen by a golden
// test (TECHSPEC §12). It consumes the SAME bus the TUI renderer consumes, so
// --json and human output are one run rendered two ways (TECHSPEC §3, §11).
//
// RenderJSON returns when the subscription channel closes (the bus is closed
// after the producing run completes). It returns the first write error
// encountered, after which it stops emitting.
func RenderJSON(w io.Writer, sub *Subscription) error {
	enc := json.NewEncoder(w)
	for ev := range sub.C {
		if err := enc.Encode(ev); err != nil {
			return fmt.Errorf("events: encode %s event: %w", ev.Type, err)
		}
	}
	return nil
}
