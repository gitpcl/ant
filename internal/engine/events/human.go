package events

import (
	"fmt"
	"io"
	"strings"

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

	case TypeAntVerified:
		if ev.AntVerified == nil {
			return nil
		}
		return renderVerified(w, *ev.AntVerified)

	case TypeAntSkipped:
		if ev.AntSkipped == nil {
			return nil
		}
		return renderSkipped(w, *ev.AntSkipped)

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
//
// SECURITY (Sprint 020 LOW, defense-in-depth): Message and Snippet can be
// SCRIPT-CONTROLLED — a `command` detector emits them on stdout, so a
// reviewed-but-malicious community species could inject ANSI / terminal-escape
// sequences that hijack the reviewer's TTY. They are sanitized before the
// terminal write (control chars stripped, \n/\t preserved). This is the same
// defense class as fix/tool.go's sanitizeVersion. The --json path is untouched
// and unaffected: json.go encodes via json.NewEncoder, which already escapes
// control bytes, so the wire contract (scout-json.golden) is byte-unchanged.
func renderFinding(w io.Writer, f engine.Finding, detail bool) error {
	if _, err := fmt.Fprintf(w, "  [%s] %s:%d:%d  %s (%s)\n",
		f.Severity, f.File, f.Span.StartLine, f.Span.StartCol, sanitizeControl(f.Message), f.Species); err != nil {
		return err
	}
	if detail && f.Snippet != "" {
		_, err := fmt.Fprintf(w, "        %s\n", sanitizeControl(f.Snippet))
		return err
	}
	return nil
}

// sanitizeControl strips non-printable control characters from a string before it
// is written to a terminal, PRESERVING newline and tab (legitimate layout). It
// removes C0 controls (incl. ESC 0x1b, the lead byte of ANSI/CSI sequences, and
// CR 0x0d) and DEL (0x7f), neutralizing terminal-escape injection from
// script-controlled finding text. Unlike fix.sanitizeVersion (which also drops
// \n/\t and truncates — correct for a one-line version string) this keeps \n/\t,
// since a finding message/snippet may legitimately span lines; the two helpers
// share the defense intent but not the semantics, so they are kept separate
// rather than coupling the events and fix packages. Operates on bytes: any byte
// >= 0x20 except DEL is kept, so valid UTF-8 multi-byte runes (all continuation/
// lead bytes are >= 0x80) pass through untouched.
func sanitizeControl(s string) string {
	if !strings.ContainsFunc(s, isStrippableControl) {
		return s // common case: nothing to strip, no allocation
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; !isStrippableControlByte(c) {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// isStrippableControlByte reports whether byte c is a control char to strip: any
// C0 control (< 0x20) other than tab/newline, or DEL (0x7f).
func isStrippableControlByte(c byte) bool {
	if c == '\n' || c == '\t' {
		return false
	}
	return c < 0x20 || c == 0x7f
}

// isStrippableControl is the rune predicate for the fast-path ContainsFunc probe;
// it mirrors isStrippableControlByte for the ASCII control range (the only range
// that matters — multi-byte runes are all >= 0x80 and never stripped).
func isStrippableControl(r rune) bool {
	return r < 0x80 && isStrippableControlByte(byte(r))
}

// renderVerified prints a one-line confirmation that an ant's fix passed
// verification and was staged, with its provenance (the Fixer string) so the
// human/TUI output shows the trust chain, mirroring what --json carries.
func renderVerified(w io.Writer, p AntVerifiedPayload) error {
	file := ""
	if len(p.Diff.Files) > 0 {
		file = p.Diff.Files[0].Path
	}
	_, err := fmt.Fprintf(w, "  verified %s — staged (%s)\n", file, p.Diff.Fixer)
	return err
}

// renderSkipped prints a skip prominently: which finding was skipped, which
// verifier gate failed, and the reason. A skip is a TRUST SIGNAL, not a hidden
// error (PRD §6.3), so it is always surfaced in human output — never swallowed —
// exactly as --json carries it on the ant.skipped payload.
func renderSkipped(w io.Writer, p AntSkippedPayload) error {
	reason := p.Reason
	if reason == "" {
		reason = "verification failed"
	}
	_, err := fmt.Fprintf(w, "  skipped %s:%d — %s failed: %s\n",
		p.Finding.File, p.Finding.Span.StartLine, p.FailedCheck.Name, reason)
	return err
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
