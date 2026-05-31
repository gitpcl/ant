package explain

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/gitpcl/ant/internal/engine"
)

// Format selects explain's output rendering, mirroring doctor.Format: Human is
// an aligned key/value layout for terminals; JSON is the single-document Detail
// contract CI and front doors parse.
type Format int

const (
	// FormatHuman is the default key/value rendering.
	FormatHuman Format = iota
	// FormatJSON emits the Detail as one indented JSON document. Like doctor and
	// `ant species list`, explain is a one-shot lookup, so it renders a single
	// self-contained object, not a bus stream.
	FormatJSON
)

// Render writes the detail to w in the chosen format. It returns any
// write/encode error so the CLI can classify it; it does not decide the exit
// code.
func Render(w io.Writer, format Format, d Detail) error {
	if format == FormatJSON {
		return renderJSON(w, d)
	}
	return renderHuman(w, d)
}

// renderJSON encodes the Detail as an indented JSON document with a trailing
// newline, matching the repo's other machine-readable outputs (doctor, species
// list).
func renderJSON(w io.Writer, d Detail) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		return fmt.Errorf("explain: encode detail: %w", err)
	}
	return nil
}

// renderHuman prints an aligned key/value block: run-level metadata for a run,
// or the located finding's fields for a finding. It uses the same tabwriter
// layout as doctor so the front door feels consistent.
func renderHuman(w io.Writer, d Detail) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	switch d.Kind {
	case KindRun:
		writeRun(tw, d.Run)
	case KindFinding:
		writeFinding(tw, d.RunID, d.Index, d.Finding)
	default:
		fmt.Fprintf(tw, "kind:\t%s\n", d.Kind)
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("explain: flush detail: %w", err)
	}
	return nil
}

// writeRun renders the run-level summary plus a one-line-per-finding index so a
// reader can pick a finding to drill into via `ant explain <runID>#<index>`.
func writeRun(tw *tabwriter.Writer, run *engine.Run) {
	if run == nil {
		fmt.Fprintln(tw, "run:\t(missing)")
		return
	}
	fmt.Fprintf(tw, "run:\t%s\n", run.ID)
	fmt.Fprintf(tw, "started:\t%s\n", run.StartedAt)
	if run.FinishedAt != "" {
		fmt.Fprintf(tw, "finished:\t%s\n", run.FinishedAt)
	}
	fmt.Fprintf(tw, "root:\t%s\n", run.Scope.Root)
	fmt.Fprintf(tw, "findings:\t%d\n", len(run.Findings))
	for i, f := range run.Findings {
		fmt.Fprintf(tw, "  [%d]\t%s\t%s\t%s:%d\t%s\n",
			i, f.Severity, f.Species, f.File, f.Span.StartLine, f.Message)
	}
}

// writeFinding renders one finding's fields, including its owning run and index
// so the output is self-describing.
func writeFinding(tw *tabwriter.Writer, runID string, index int, f *engine.Finding) {
	if f == nil {
		fmt.Fprintln(tw, "finding:\t(missing)")
		return
	}
	fmt.Fprintf(tw, "run:\t%s\n", runID)
	fmt.Fprintf(tw, "index:\t%d\n", index)
	fmt.Fprintf(tw, "species:\t%s\n", f.Species)
	fmt.Fprintf(tw, "severity:\t%s\n", f.Severity)
	fmt.Fprintf(tw, "file:\t%s\n", f.File)
	fmt.Fprintf(tw, "span:\t%d:%d-%d:%d\n", f.Span.StartLine, f.Span.StartCol, f.Span.EndLine, f.Span.EndCol)
	fmt.Fprintf(tw, "message:\t%s\n", f.Message)
	if f.Snippet != "" {
		fmt.Fprintf(tw, "snippet:\t%s\n", f.Snippet)
	}
}
