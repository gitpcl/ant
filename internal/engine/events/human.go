package events

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/gitpcl/ant/internal/engine"
)

// HumanOptions toggles the human renderer. Detail adds the per-finding code
// snippet (scout --detail). All lists every finding one per line — the full flat
// list (scout --all) — instead of the default severity-led DIGEST. They compose:
// All+Detail is the flat list with snippets.
type HumanOptions struct {
	Detail bool
	All    bool
}

// RenderHuman drains a subscription and writes a plain-text rendering of the
// same event stream RenderJSON consumes — the human and --json outputs are one
// run rendered two ways (TECHSPEC §3, §11). The function returns when the
// subscription channel closes.
//
// Scout's findings are BUFFERED (not streamed) so run.end can emit a
// severity-led digest: the high findings in full, medium/low folded to per-
// species counts (PRD UX fix — a flat 1268-line dump is "hard to digest").
// --all (opts.All) restores the full one-per-line flat list. The fix path is
// unaffected: ant.verified / ant.skipped are TRUST SIGNALS and still stream LIVE
// as they arrive (a fix run emits no bare finding list to buffer), so a fix run's
// human output is byte-for-byte unchanged.
//
// Rendering lives in the engine, not cmd/ant, because the CLI boundary forbids
// hand-rolled output formatting that should derive from the single-source-of-
// truth event bus. cmd/ant only chooses which renderer to attach.
func RenderHuman(w io.Writer, sub *Subscription, opts HumanOptions) error {
	var findings []engine.Finding
	// root is captured from run.start so the digest header shows the ACTUAL scanned
	// path. It is kept purely in this renderer's buffered state — NOT on
	// RunEndPayload — so the --json wire contract (scout-json.golden) stays
	// byte-identical (threat-model fix: a hardcoded "." misreported subtree scans).
	root := "."
	for ev := range sub.C {
		switch ev.Type {
		case TypeDetectFinding:
			// Buffer findings; the digest (or the --all flat list) is emitted at
			// run.end. This is scout-only — scout emits findings + run.end, so
			// buffering changes no live trust signal.
			if ev.DetectFinding != nil {
				findings = append(findings, ev.DetectFinding.Finding)
			}
		case TypeRunEnd:
			if ev.RunEnd == nil {
				continue
			}
			if err := renderScoutClose(w, findings, *ev.RunEnd, root, opts); err != nil {
				return err
			}
		case TypeRunStart:
			// Capture the scanned root for the digest header before deciding whether
			// to print the streaming banner.
			if ev.RunStart != nil && ev.RunStart.Scope.Root != "" {
				root = ev.RunStart.Scope.Root
			}
			// In digest mode the digest header ("ant scout · scanned …") replaces the
			// "scanning <root>" banner, so suppress it; --all keeps the banner so its
			// flat-list output is unchanged from the pre-digest behavior.
			if opts.All {
				if err := renderStreamingEvent(w, ev); err != nil {
					return err
				}
			}
		default:
			// ant.verified, ant.skipped, etc. stream live, unchanged.
			if err := renderStreamingEvent(w, ev); err != nil {
				return err
			}
		}
	}
	return nil
}

// renderStreamingEvent renders the events that are NOT buffered: the live trust
// signals of a fix run (ant.verified / ant.skipped) and the run banner. These
// keep their original arrival-ordered behavior so a fix run's output is unchanged.
func renderStreamingEvent(w io.Writer, ev Event) error {
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
	}
	return nil
}

// renderScoutClose renders the end of a scout run from the BUFFERED findings: the
// severity-led digest by default, or — with opts.All — the full flat one-per-line
// list followed by the summary. An aborted or zero-finding run defers to the
// existing renderSummary path unchanged.
func renderScoutClose(w io.Writer, findings []engine.Finding, end RunEndPayload, root string, opts HumanOptions) error {
	if end.Error != "" || len(findings) == 0 {
		// Aborted run (renderSummary prints nothing) or clean run (No findings. /
		// Nothing was modified.) — both keep the existing behavior exactly.
		return renderSummary(w, end)
	}
	if opts.All {
		return renderFlatList(w, findings, end, opts.Detail)
	}
	return renderDigest(w, findings, root, end)
}

// renderFlatList restores the pre-digest behavior: every finding, one per line,
// sorted high→low severity for ordering (every line preserved), then the summary.
// --detail still composes (adds the snippet). This is `ant scout --all`.
func renderFlatList(w io.Writer, findings []engine.Finding, end RunEndPayload, detail bool) error {
	sorted := sortedBySeverity(findings)
	for _, f := range sorted {
		if err := renderFinding(w, f, detail); err != nil {
			return err
		}
	}
	return renderSummary(w, end)
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

// maxDigestSpecies caps how many medium/low species the digest lists before it
// collapses the remainder into a "+ K more species" line, keeping the digest
// scannable (PRD UX fix). High findings are NEVER capped — they are always listed
// in full.
const maxDigestSpecies = 8

// renderDigest writes the severity-led DIGEST: a header with the per-severity
// breakdown, every HIGH finding in full (sorted by path for stability), the
// medium/low findings folded to per-species counts (sorted by count desc, species
// asc), then the action footer and the mandatory "Nothing was modified." line
// (PRD §6.1). It is scout-specific — the finding dump rendered digestibly.
//
// SECURITY: every piece of finding text rendered here (Species, File, Message)
// is passed through sanitizeControl, preserving the Sprint-020 terminal-escape-
// injection defense for the new digest surface — a command detector's output is
// script-controlled.
func renderDigest(w io.Writer, findings []engine.Finding, root string, end RunEndPayload) error {
	high, medLow := partitionBySeverity(findings)

	// Header: "ant scout · scanned <root> · <N> findings". root is captured from the
	// run.start event (buffered in RenderHuman), so a subtree scan reports the real
	// path; it falls back to "." when unset. It is NOT carried on RunEndPayload, so
	// the --json wire contract stays byte-identical.
	if root == "" {
		root = "."
	}
	if _, err := fmt.Fprintf(w, "ant scout · scanned %s · %d findings\n\n", sanitizeControl(root), len(findings)); err != nil {
		return err
	}

	// Per-severity breakdown; omit a tier with 0.
	counts := severityCounts(findings)
	for _, tier := range []struct {
		name  string
		sev   engine.Severity
		extra string
	}{
		{"high", engine.SeverityHigh, "   ← act on these first"},
		{"medium", engine.SeverityMedium, ""},
		{"low", engine.SeverityLow, ""},
	} {
		n := counts[tier.sev]
		if n == 0 {
			continue
		}
		if _, err := fmt.Fprintf(w, "  %-8s %d%s\n", tier.name, n, tier.extra); err != nil {
			return err
		}
	}

	// HIGH block: every high finding in full, sorted by file path for stability.
	if len(high) > 0 {
		if _, err := fmt.Fprintf(w, "\nHIGH (%d)\n", len(high)); err != nil {
			return err
		}
		sort.SliceStable(high, func(i, j int) bool {
			if high[i].File != high[j].File {
				return high[i].File < high[j].File
			}
			return high[i].Span.StartLine < high[j].Span.StartLine
		})
		for _, f := range high {
			loc := fmt.Sprintf("%s:%d", sanitizeControl(displayPath(f.File)), f.Span.StartLine)
			if _, err := fmt.Fprintf(w, "  %-36s %s\n", loc, sanitizeControl(f.Species)); err != nil {
				return err
			}
		}
	}

	// TOP SPECIES block: medium/low folded to per-species counts.
	if len(medLow) > 0 {
		if _, err := fmt.Fprintf(w, "\nTOP SPECIES (medium / low)\n"); err != nil {
			return err
		}
		if err := renderSpeciesCounts(w, medLow); err != nil {
			return err
		}
	}

	// Footer: the --all hint (only when medium/low were folded away), the fix hint,
	// and the mandatory read-only statement.
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if len(medLow) > 0 {
		if _, err := fmt.Fprintf(w, "→ ant scout --all   list every finding\n"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "→ ant fix           propose fixes\n"); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, "Nothing was modified.")
	return err
}

// renderSpeciesCounts writes the per-species tallies for the medium/low set,
// sorted by count descending then species name ascending, capped at
// maxDigestSpecies with a "+ K more species" remainder line.
func renderSpeciesCounts(w io.Writer, findings []engine.Finding) error {
	type bucket struct {
		species string
		count   int
	}
	byName := map[string]int{}
	for _, f := range findings {
		byName[f.Species]++
	}
	buckets := make([]bucket, 0, len(byName))
	for s, c := range byName {
		buckets = append(buckets, bucket{species: s, count: c})
	}
	sort.SliceStable(buckets, func(i, j int) bool {
		if buckets[i].count != buckets[j].count {
			return buckets[i].count > buckets[j].count
		}
		return buckets[i].species < buckets[j].species
	})

	shown := buckets
	if len(buckets) > maxDigestSpecies {
		shown = buckets[:maxDigestSpecies]
	}
	for _, b := range shown {
		if _, err := fmt.Fprintf(w, "  %-24s %d\n", sanitizeControl(b.species), b.count); err != nil {
			return err
		}
	}
	if rest := len(buckets) - len(shown); rest > 0 {
		if _, err := fmt.Fprintf(w, "  + %d more species\n", rest); err != nil {
			return err
		}
	}
	return nil
}

// displayPath trims a leading "./" so a finding's File renders as "internal/x.go"
// rather than "./internal/x.go" in the digest, matching the target UX format.
func displayPath(p string) string {
	return strings.TrimPrefix(p, "./")
}

// partitionBySeverity splits findings into the high set and the medium/low set
// (everything not high — medium, low, and any unknown). The inputs are not
// mutated; two fresh slices are returned.
func partitionBySeverity(findings []engine.Finding) (high, medLow []engine.Finding) {
	for _, f := range findings {
		if f.Severity == engine.SeverityHigh {
			high = append(high, f)
		} else {
			medLow = append(medLow, f)
		}
	}
	return high, medLow
}

// severityCounts tallies findings per severity for the breakdown header.
func severityCounts(findings []engine.Finding) map[engine.Severity]int {
	counts := map[engine.Severity]int{}
	for _, f := range findings {
		counts[f.Severity]++
	}
	return counts
}

// sortedBySeverity returns a new slice ordered high→low severity, then by file
// path and line for stable ordering within a tier. The input is not mutated.
func sortedBySeverity(findings []engine.Finding) []engine.Finding {
	out := make([]engine.Finding, len(findings))
	copy(out, findings)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity > out[j].Severity
		}
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Span.StartLine < out[j].Span.StartLine
	})
	return out
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
