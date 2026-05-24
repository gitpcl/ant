package apply

import (
	"context"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/events"
)

// Applier adapts Land to the colony.Applier interface so `ant fix --apply` can
// fuse apply for trusted species through the driver without the driver importing
// go-git (the driver depends only on its own colony.Applier interface; the CLI
// injects this concrete implementation). It carries the apply Options (root,
// branch policy) the fix run resolved from flags.
type Applier struct {
	Opts Options
}

// NewApplier builds an Applier landing into opts.Root with opts' branch policy.
func NewApplier(opts Options) *Applier {
	return &Applier{Opts: opts}
}

// ApplyRecords lands the given (already trust-filtered) records and emits
// apply.done per landed diff, returning the count applied. It satisfies
// colony.Applier. The caller (the fix driver's fuseApply) has already filtered
// the records to species whose effective auto_apply is true (ADR-0002), so this
// applies all of them.
func (a *Applier) ApplyRecords(ctx context.Context, bus *events.Bus, runID string, records []engine.StagedRecord) (int, error) {
	if len(records) == 0 {
		return 0, nil
	}
	res, err := Land(ctx, bus, runID, records, a.Opts)
	if err != nil {
		return len(res.Commits), err
	}
	return len(res.Commits), nil
}
