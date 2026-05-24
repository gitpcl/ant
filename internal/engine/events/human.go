package events

import (
	"fmt"
	"io"

	"github.com/gitpcl/ant/internal/engine"
)

// RenderHuman drains a subscription and writes a plain-text rendering of the
// same event stream RenderJSON consumes — the human and --json outputs are one
// run rendered two ways (TECHSPEC §3, §11). detail toggles per-finding verbosity
// (the scout --detail flag). The function returns when the subscription channel
// closes.
//
// Rendering lives in the engine, not cmd/ant, because the CLI boundary forbids
// hand-rolled output formatting that should derive from the single-source-of-
// truth event bus. cmd/ant only chooses which renderer to attach.
func RenderHuman(w io.Writer, sub *Subscription, detail bool) error {
	for ev := range sub.C {
		if err := renderHumanEvent(w, ev, detail); err != nil {
			return err
		}
	}
	return nil
}

// renderHumanEvent renders a single event. Findings are listed as they arrive;
// run.end prints the summary line and the explicit "nothing was modified"
// statement that the bare-`ant` / scout UX requires (PRD §6.1, ADR 0001).
func renderHumanEvent(w io.Writer, ev Event, detail bool) error {
	switch ev.Type {
	case TypeRunStart:
		if ev.RunStart == nil {
			return nil
		}
		root := ev.RunStart.Scope.Root
		if root == "" {
			root = "."
		}
		_, err := fmt.Fprintf(w, "ant scout: scanning %s\n", root)
		return err

	case TypeDetectFinding:
		if ev.DetectFinding == nil {
			return nil
		}
		return renderFinding(w, ev.DetectFinding.Finding, detail)

	case TypeRunEnd:
		if ev.RunEnd == nil {
			return nil
		}
		return renderSummary(w, *ev.RunEnd)
	}
	return nil
}

// renderFinding prints one finding. The compact form is a single line; --detail
// adds the snippet and the owning species' rule provenance.
func renderFinding(w io.Writer, f engine.Finding, detail bool) error {
	if _, err := fmt.Fprintf(w, "  [%s] %s:%d:%d  %s (%s)\n",
		f.Severity, f.File, f.Span.StartLine, f.Span.StartCol, f.Message, f.Species); err != nil {
		return err
	}
	if detail && f.Snippet != "" {
		_, err := fmt.Fprintf(w, "        %s\n", f.Snippet)
		return err
	}
	return nil
}

// renderSummary prints the closing summary. It always states that nothing was
// modified — scout (and bare `ant`) are read-only, and saying so explicitly is
// a product requirement (PRD §6.1: a new user must see that running ant changed
// nothing).
func renderSummary(w io.Writer, end RunEndPayload) error {
	if end.Error != "" {
		// Aborted run: print nothing here. The CLI boundary surfaces the error on
		// stderr (single diagnostic), and --json carries it in run.end.error.
		// Crucially we must NOT print the "No findings / Nothing was modified"
		// clean-scan summary, which would mislead.
		return nil
	}
	noun := "findings"
	if end.Findings == 1 {
		noun = "finding"
	}
	if end.Findings == 0 {
		if _, err := fmt.Fprintf(w, "\nNo findings.\n"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(w, "\n%d %s (highest severity: %s).\n",
			end.Findings, noun, end.HighestSeverity); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "Run `ant fix` to propose fixes.\n"); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, "Nothing was modified.")
	return err
}
