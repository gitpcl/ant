package detect

import (
	"context"

	"github.com/gitpcl/ant/internal/engine"
)

// NewRecorded builds an ast-grep detector that, instead of shelling out, replays
// a recorded ast-grep JSON payload through the exact same parse + mapping path
// the live adapter uses. It exists so scout, golden --json, and other
// engine-level tests can exercise the real Finding mapping deterministically
// without a live ast-grep binary (TECHSPEC §12 — CI needs no installed matcher).
//
// It is production-grade code (not test-only) so it can also back the
// fixture-driven demo path: the same recorded contract that proves the parser
// also feeds reproducible runs.
func NewRecorded(species string, output []byte) engine.Detector {
	return NewASTGrep(species, "recorded", withRunner(
		func(context.Context, string, []string) ([]byte, error) {
			return output, nil
		},
	))
}
