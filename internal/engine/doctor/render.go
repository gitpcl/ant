package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
)

// Format selects doctor's output rendering. Human is an aligned table for
// terminals; JSON is the single-document report contract CI parses.
type Format int

const (
	// FormatHuman is the default aligned-table rendering.
	FormatHuman Format = iota
	// FormatJSON emits the Report as one indented JSON document. Unlike the run
	// event stream (JSON Lines), doctor is a one-shot probe — like `ant species
	// list` — so it renders a single self-contained object, not a bus stream.
	FormatJSON
)

// Render writes the report to w in the chosen format. It returns any write/encode
// error so the CLI can classify it; it does not decide the exit code (the caller
// reads Report.Ready for that).
func Render(w io.Writer, format Format, report Report) error {
	if format == FormatJSON {
		return renderJSON(w, report)
	}
	return renderHuman(w, report)
}

// renderJSON encodes the Report as an indented JSON document with a trailing
// newline, matching the repo's other machine-readable outputs.
func renderJSON(w io.Writer, report Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return fmt.Errorf("doctor: encode report: %w", err)
	}
	return nil
}

// renderHuman prints one aligned row per check (STATUS, NAME, DETAIL) followed
// by a one-line readiness summary, mirroring `ant species list`'s tabwriter
// layout so the front door feels consistent.
func renderHuman(w io.Writer, report Report) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tCHECK\tDETAIL")
	for _, c := range report.Checks {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", statusGlyph(c), c.Name, c.Detail)
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("doctor: flush report: %w", err)
	}
	if report.Ready {
		fmt.Fprintln(w, "\nready: environment is ready to run ant")
	} else {
		fmt.Fprintln(w, "\nnot ready: a required capability is missing (see fail rows above)")
	}
	return nil
}

// statusGlyph renders a check's status as a short text token; a required-but-
// failing check is marked distinctly from an advisory warning so the human
// reader sees which rows actually block readiness.
func statusGlyph(c Check) string {
	switch c.Status {
	case StatusOK:
		return "ok"
	case StatusWarn:
		return "warn"
	case StatusFail:
		if c.Required {
			return "FAIL"
		}
		return "warn"
	default:
		return string(c.Status)
	}
}

// plural renders "<n> <singular|plural>" picking the form by count.
func plural(n int, singular, plural string) string {
	if n == 1 {
		return strconv.Itoa(n) + " " + singular
	}
	return strconv.Itoa(n) + " " + plural
}

// joinWarnings concatenates warning lines into a single comma-separated detail
// string for the config check.
func joinWarnings(warnings []string) string {
	return strings.Join(warnings, "; ")
}
