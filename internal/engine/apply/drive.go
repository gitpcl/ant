package apply

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/events"
	"github.com/gitpcl/ant/internal/engine/stage"
)

// DriveOptions parameterizes the `ant apply` entry point.
type DriveOptions struct {
	Root     string // git working-tree root to land into
	RunID    string // the run whose accepted diffs to apply
	NoBranch bool   // land on the current branch instead of a new one
	JSON     bool   // emit the --json event stream instead of human output
}

// Drive is the `ant apply` engine entry point the CLI calls. It loads the run's
// staged records, filters to the ACCEPTED set (review marks), and lands exactly
// those via go-git, framing the work in run.start … apply.done* … run.end so the
// --json stream is well-formed (TECHSPEC §11, §12) and the human path reports
// the landing. It owns the bus + renderer goroutine so cmd/ant stays thin (the
// boundary test forbids those constructs in the CLI).
//
// Unaccepted/unmarked diffs are NOT applied — only MarkAccepted records land
// (review-interaction.md §1). Nothing to apply is success (exit 0).
func Drive(ctx context.Context, w io.Writer, area *stage.Area, opts DriveOptions) error {
	records, err := area.ListRecords()
	if err != nil {
		if isRunNotFound(err) {
			// No run / nothing staged: nothing to apply is success, not an error.
			_, werr := io.WriteString(w, "Nothing to apply: no staged diffs were found.\n")
			return werr
		}
		return fmt.Errorf("%w: load staged diffs for apply: %v", engine.ErrOperational, err)
	}

	accepted := acceptedRecords(records)

	bus := events.NewBus()
	sub := bus.Subscribe()
	renderErr := make(chan error, 1)
	go func() {
		if opts.JSON {
			renderErr <- events.RenderJSON(w, sub)
		} else {
			renderErr <- renderApplyHuman(w, sub)
		}
	}()

	landErr := driveApply(ctx, bus, accepted, opts)
	bus.Close()
	rErr := <-renderErr

	if landErr != nil {
		return landErr
	}
	return rErr
}

// driveApply publishes the run framing and lands the accepted records. It emits
// run.start, lets Land emit apply.done per landed diff, then run.end with the
// applied count and the branch (for the summary line).
func driveApply(ctx context.Context, bus *events.Bus, accepted []engine.StagedRecord, opts DriveOptions) error {
	bus.Publish(events.Event{Type: events.TypeRunStart, RunStart: &events.RunStartPayload{
		RunID: opts.RunID, Scope: engine.Scope{Root: opts.Root}}})

	if len(accepted) == 0 {
		bus.Publish(events.Event{Type: events.TypeRunEnd, RunEnd: &events.RunEndPayload{
			RunID: opts.RunID, Applied: 0, HighestSeverity: engine.SeverityUnknown.String()}})
		return nil
	}

	res, err := Land(ctx, bus, opts.RunID, accepted, Options{Root: opts.Root, NoBranch: opts.NoBranch})

	runEnd := &events.RunEndPayload{
		RunID:           opts.RunID,
		Applied:         len(res.Commits),
		HighestSeverity: engine.SeverityUnknown.String(),
	}
	if err != nil {
		runEnd.Error = err.Error()
	}
	bus.Publish(events.Event{Type: events.TypeRunEnd, RunEnd: runEnd})
	return err
}

// acceptedRecords returns only the records a reviewer accepted (MarkAccepted),
// so apply lands exactly the accepted set and never an unmarked/skipped diff.
func acceptedRecords(records []engine.StagedRecord) []engine.StagedRecord {
	out := make([]engine.StagedRecord, 0, len(records))
	for _, rec := range records {
		if rec.Mark == engine.MarkAccepted {
			out = append(out, rec)
		}
	}
	return out
}

// isRunNotFound reports whether err is the Store's "no such run" sentinel.
func isRunNotFound(err error) bool {
	return errors.Is(err, engine.ErrRunNotFound)
}

// renderApplyHuman is the plain-text rendering of the apply event stream (the
// human counterpart to RenderJSON). It reports each landed diff and the closing
// summary, consuming the SAME bus the --json renderer does (TECHSPEC §3, §11).
func renderApplyHuman(w io.Writer, sub *events.Subscription) error {
	branch := ""
	for ev := range sub.C {
		switch ev.Type {
		case events.TypeRunStart:
			if _, err := fmt.Fprintln(w, "ant apply: landing accepted diffs"); err != nil {
				return err
			}
		case events.TypeApplyDone:
			if ev.ApplyDone == nil {
				continue
			}
			branch = ev.ApplyDone.Branch
			where := "current branch"
			if branch != "" {
				where = "branch " + branch
			}
			if _, err := fmt.Fprintf(w, "  applied %s → %s (%s)\n",
				ev.ApplyDone.Path, where, shortCommit(ev.ApplyDone.Commit)); err != nil {
				return err
			}
		case events.TypeRunEnd:
			if ev.RunEnd == nil {
				continue
			}
			if ev.RunEnd.Error != "" {
				if _, err := fmt.Fprintf(w, "apply failed: %s\n", ev.RunEnd.Error); err != nil {
					return err
				}
				continue
			}
			if ev.RunEnd.Applied == 0 {
				if _, err := fmt.Fprintln(w, "Nothing to apply: no accepted diffs. Run `ant review` to accept some."); err != nil {
					return err
				}
				continue
			}
			where := "the current branch"
			if branch != "" {
				where = "branch " + branch
			}
			if _, err := fmt.Fprintf(w, "\nApplied %d diff(s) to %s.\n", ev.RunEnd.Applied, where); err != nil {
				return err
			}
		}
	}
	return nil
}

// shortCommit renders the first 7 chars of a commit hash for the human summary.
func shortCommit(hash string) string {
	if len(hash) <= 7 {
		return hash
	}
	return hash[:7]
}
