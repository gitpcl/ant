package review

import (
	"fmt"
	"strings"

	"github.com/gitpcl/ant/internal/engine"
)

// View renders the current review screen (review-interaction.md §2). It is a
// pure projection of the model — Update owns all state change.
func (m model) View() string {
	switch m.phase {
	case phaseEmpty:
		return m.emptyView()
	case phaseEnd:
		return m.endView()
	case phaseConfirmQuit:
		return m.confirmQuitView()
	default:
		return m.walkView()
	}
}

// walkView draws the four regions for the current item (review-interaction.md
// §2): header, provenance, diff-or-explain pane, verb bar.
func (m model) walkView() string {
	if m.showHelp {
		return m.helpView()
	}
	it := m.items[m.cursor]
	var b strings.Builder
	m.renderHeader(&b, it)
	b.WriteString("\n")
	m.renderProvenance(&b, it)
	b.WriteString("\n")
	if m.showExpl {
		m.renderExplain(&b, it)
	} else {
		m.renderDiff(&b, it)
	}
	b.WriteString("\n")
	m.renderVerbBar(&b)
	return b.String()
}

// renderHeader draws Region A: position counter, accepted/skipped tally
// (review-interaction.md §2.1).
func (m model) renderHeader(b *strings.Builder, it reviewItem) {
	accepted, skipped, _ := m.counts()
	fmt.Fprintf(b, " ant review · run %s · diff %d / %d            ▸ %d accepted · %d skipped\n",
		short(m.runID), m.cursor+1, len(m.items), accepted, skipped)
	_ = it
}

// renderProvenance draws Region B, the always-visible PROVENANCE panel (PRD
// §6.4, review-interaction.md §3): species + severity, finding message, fixer,
// and every verifier that passed.
func (m model) renderProvenance(b *strings.Builder, it reviewItem) {
	lines := []string{
		fmt.Sprintf("  species   %-18s severity  %s", it.finding.Species, m.severityBadge(it.finding.Severity)),
		fmt.Sprintf("  finding   %s", it.finding.Message),
		fmt.Sprintf("  fixer     %s", it.diff.Fixer),
		fmt.Sprintf("  verified  %s", m.verifiersLine(it.verify)),
	}
	b.WriteString(m.titledBox("PROVENANCE", "", lines))
}

// verifiersLine renders one "✔ name (detail)" per PASSED check, in run order
// (diff-bounded first — review-interaction.md §3). Only passed checks appear: a
// staged diff passed every required verifier by definition.
func (m model) verifiersLine(vr engine.VerifyResult) string {
	parts := make([]string, 0, len(vr.Checks))
	for _, c := range vr.Checks {
		if !c.Passed {
			continue
		}
		label := m.styles.verified.Render(m.glyphs.Verified) + " " + c.Name
		if c.Detail != "" && isStrategyDetail(c.Detail) {
			label += " (" + c.Detail + ")"
		}
		parts = append(parts, label)
	}
	if len(parts) == 0 {
		return "(none recorded)"
	}
	return strings.Join(parts, "   ")
}

// renderDiff draws Region C in diff mode (review-interaction.md §7). Collapsed:
// the first hunk of the first file + a "more" footer. Expanded: the full
// concatenated patch across files, scrollable.
func (m model) renderDiff(b *strings.Builder, it reviewItem) {
	files := it.diff.Files
	title := "DIFF"
	if len(files) > 0 {
		title = "DIFF · " + files[0].Path
	}
	hint := "[d expand]"
	if m.showFull {
		hint = "[d collapse]"
	}

	var body []string
	if m.showFull {
		body = m.fullDiffLines(files)
		body = m.applyScroll(body)
	} else {
		body, _ = m.collapsedDiffLines(files)
	}
	b.WriteString(m.titledBox(title, hint, body))
}

// collapsedDiffLines returns the first hunk of the first file, colorized, plus a
// footer when more hunks/files exist (review-interaction.md §7).
func (m model) collapsedDiffLines(files []engine.FileDiff) ([]string, bool) {
	if len(files) == 0 {
		return []string{"  (no file changes)"}, false
	}
	hunks := splitIntoHunks(files[0].Patch)
	var out []string
	if len(hunks) > 0 {
		out = m.colorizeDiff(hunks[0])
	} else {
		out = m.colorizeDiff(files[0].Patch)
	}
	moreHunks := 0
	if len(hunks) > 1 {
		moreHunks = len(hunks) - 1
	}
	moreFiles := len(files) - 1
	if moreHunks > 0 || moreFiles > 0 {
		out = append(out, fmt.Sprintf("  … %d more hunks across %d files — d to expand", moreHunks, len(files)))
	}
	return out, moreHunks > 0 || moreFiles > 0
}

// fullDiffLines returns every file's patch concatenated with a file separator,
// colorized (review-interaction.md §7 expanded mode).
func (m model) fullDiffLines(files []engine.FileDiff) []string {
	var out []string
	for _, fd := range files {
		out = append(out, m.styles.chrome.Render(fmt.Sprintf("  ── %s ──", fd.Path)))
		out = append(out, m.colorizeDiff(fd.Patch)...)
	}
	if len(out) == 0 {
		out = []string{"  (no file changes)"}
	}
	return out
}

// colorizeDiff colors each unified-diff line by its leading +/-/space and draws
// @@ headers in chrome (review-interaction.md §7). The +/- prefix is preserved so
// the diff is readable with color off. File headers (---/+++) are dropped.
func (m model) colorizeDiff(patch string) []string {
	var out []string
	for _, ln := range strings.Split(strings.TrimRight(patch, "\n"), "\n") {
		switch {
		case strings.HasPrefix(ln, "--- "), strings.HasPrefix(ln, "+++ "):
			continue
		case strings.HasPrefix(ln, "@@"):
			out = append(out, "  "+m.styles.chrome.Render(ln))
		case strings.HasPrefix(ln, "+"):
			out = append(out, "  "+m.styles.diffAdd.Render(ln))
		case strings.HasPrefix(ln, "-"):
			out = append(out, "  "+m.styles.diffDel.Render(ln))
		default:
			out = append(out, "  "+m.styles.diffCtx.Render(ln))
		}
	}
	return out
}

// applyScroll windows the diff lines by the scroll offset (expanded mode, §4).
func (m model) applyScroll(lines []string) []string {
	const window = 12
	if m.scroll <= 0 || len(lines) <= window {
		if len(lines) > window {
			return lines[:window]
		}
		return lines
	}
	start := m.scroll
	if start > len(lines)-1 {
		start = len(lines) - 1
	}
	end := start + window
	if end > len(lines) {
		end = len(lines)
	}
	return lines[start:end]
}

// maxScroll is the deepest scroll offset for the current item's expanded diff.
func (m model) maxScroll() int {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return 0
	}
	n := len(m.fullDiffLines(m.items[m.cursor].diff.Files))
	if n < 1 {
		return 0
	}
	return n - 1
}

// renderExplain draws Region C in explain mode: the ProposedDiff.Rationale, with
// the deterministic-fix fallback when empty (review-interaction.md §2.2).
func (m model) renderExplain(b *strings.Builder, it reviewItem) {
	rationale := it.diff.Rationale
	if strings.TrimSpace(rationale) == "" {
		rationale = fmt.Sprintf("No rationale recorded — this was a deterministic fix (%s).", it.diff.Fixer)
	}
	body := wrapLines("  "+rationale, 72)
	b.WriteString(m.titledBox("EXPLAIN — why this fix", "[e hide]", body))
}

// renderVerbBar draws Region D: the six primary verbs, always visible, with the
// toggled-state annotations (review-interaction.md §2, §4).
func (m model) renderVerbBar(b *strings.Builder) {
	diff := "d diff"
	if m.showFull {
		diff = "d diff (expanded)"
	}
	expl := "e explain"
	if m.showExpl {
		expl = "e explain (showing)"
	}
	fmt.Fprintf(b, " %s\n", m.styles.chrome.Render(
		fmt.Sprintf("a accept   s skip   %s   %s   n next   q quit", diff, expl)))
	fmt.Fprintf(b, " %s\n", m.styles.chrome.Render("(? help: p previous · ↑↓ scroll · g/G top/bottom)"))
}

// emptyView is the no-staged-diffs screen (review-interaction.md §5.1).
func (m model) emptyView() string {
	var b strings.Builder
	b.WriteString(" ant review · nothing to review\n\n")
	b.WriteString(" No staged diffs were found.\n\n")
	b.WriteString(" Run `ant fix` first to produce verified diffs, then `ant review` to walk them.\n")
	b.WriteString(" (If `ant fix` ran but staged nothing, every fix was skipped — see its summary.)\n")
	return b.String()
}

// endView is the review-complete End screen (review-interaction.md §5.2).
func (m model) endView() string {
	accepted, skipped, pending := m.counts()
	var b strings.Builder
	fmt.Fprintf(&b, " ant review · run %s · done\n\n", short(m.runID))
	box := []string{
		fmt.Sprintf("  reviewed   %d", len(m.items)),
		fmt.Sprintf("  %s accepted %d   (will be applied)", m.styles.accepted.Render(m.glyphs.Accepted), accepted),
		fmt.Sprintf("  %s skipped  %d   (left staged, not applied)", m.styles.skipMark.Render(m.glyphs.SkipMark), skipped),
		fmt.Sprintf("  %s pending  %d   (no decision — not applied)", m.styles.pending.Render(m.glyphs.Pending), pending),
	}
	b.WriteString(m.titledBox("REVIEW COMPLETE", "", box))
	b.WriteString("\n")
	if accepted == 0 {
		b.WriteString(" No diffs accepted — `ant apply` would land nothing.\n")
	} else {
		fmt.Fprintf(&b, " %d diffs accepted. Run `ant apply` to land them on a branch.\n", accepted)
	}
	b.WriteString(" p  go back to review pending/skipped items      q  quit\n")
	return b.String()
}

// confirmQuitView is the quit-with-pending confirm prompt (review-interaction.md
// §5.3). Reached only when q is pressed with pending items.
func (m model) confirmQuitView() string {
	_, _, pending := m.counts()
	var b strings.Builder
	fmt.Fprintf(&b, " %s %d diffs are still pending (no accept/skip decision).\n",
		m.styles.warn.Render(m.glyphs.Warn), pending)
	b.WriteString("   Pending diffs are NOT applied.\n\n")
	b.WriteString(" q  quit anyway      r  resume review      a  accept all pending\n")
	return b.String()
}

// helpView is the full keybinding overlay (review-interaction.md §4), toggled by ?.
func (m model) helpView() string {
	var b strings.Builder
	b.WriteString(" ant review · keybindings\n\n")
	for _, row := range [][2]string{
		{"a", "accept — mark accepted, auto-advance"},
		{"s", "skip — mark skipped, auto-advance"},
		{"d", "diff — toggle collapsed / expanded"},
		{"e", "explain — toggle the rationale pane"},
		{"n", "next — advance, leave mark pending"},
		{"p", "previous — go back one item"},
		{"q", "quit — confirm if any pending remain"},
		{"↑/k ↓/j", "scroll the expanded diff"},
		{"g / G", "jump diff to top / bottom"},
		{"?", "toggle this help"},
		{"Ctrl-C", "force quit (keeps persisted marks)"},
	} {
		fmt.Fprintf(&b, "  %-10s %s\n", row[0], row[1])
	}
	b.WriteString("\n ? to close help\n")
	return b.String()
}

// --- helpers ------------------------------------------------------------------

// severityBadge renders the colored "● LEVEL" badge (review-interaction.md §3,
// §6). The level word disambiguates so the badge is legible with color off.
func (m model) severityBadge(sev engine.Severity) string {
	word := strings.ToUpper(sev.String())
	dot := m.glyphs.SevDot
	switch sev {
	case engine.SeverityHigh:
		return m.styles.sevHigh.Render(dot + " " + word)
	case engine.SeverityMedium:
		return m.styles.sevMed.Render(dot + " " + word)
	case engine.SeverityLow:
		return m.styles.sevLow.Render(dot + " " + word)
	default:
		return m.styles.pending.Render(dot + " " + word)
	}
}

// titledBox draws a bordered box with a left title and an optional right hint
// (review-interaction.md §2.1 / §3 panels). Width is fixed for an 80-col floor.
func (m model) titledBox(title, hint string, lines []string) string {
	const width = 74
	var b strings.Builder
	left := "─ " + title + " "
	right := ""
	if hint != "" {
		right = " " + hint + " ─"
	}
	dashes := width - len(left) - len(right)
	if dashes < 0 {
		dashes = 0
	}
	top := " ╭" + left + strings.Repeat("─", dashes) + right + "╮"
	b.WriteString(m.styles.chrome.Render(top) + "\n")
	for _, ln := range lines {
		b.WriteString(m.styles.chrome.Render(" │") + ln + "\n")
	}
	b.WriteString(m.styles.chrome.Render(" ╰"+strings.Repeat("─", width)+"╯") + "\n")
	return b.String()
}

// short returns the first 6 runes of an id for the compact header.
func short(s string) string {
	if len(s) <= 6 {
		return s
	}
	return s[:6]
}

// isStrategyDetail reports whether a CheckResult.Detail looks like a selection
// strategy worth surfacing parenthesized (e.g. tests:affected's strategy —
// TECHSPEC §5.3.1, review-interaction.md §3). Long prose details are not appended
// to keep the provenance line scannable.
func isStrategyDetail(detail string) bool {
	return len(detail) > 0 && len(detail) <= 24 && !strings.ContainsAny(detail, ".\n")
}

// splitIntoHunks splits a unified-diff patch into per-hunk strings (each starting
// at an @@ header), for the collapsed-diff first-hunk view.
func splitIntoHunks(patch string) []string {
	var hunks []string
	var cur strings.Builder
	started := false
	flush := func() {
		if started {
			hunks = append(hunks, cur.String())
			cur.Reset()
		}
	}
	for _, ln := range strings.Split(patch, "\n") {
		if strings.HasPrefix(ln, "@@") {
			flush()
			started = true
		}
		if started {
			cur.WriteString(ln + "\n")
		}
	}
	flush()
	return hunks
}

// wrapLines hard-wraps a string to width columns, returning the wrapped lines
// with a 2-space indent preserved (explain pane prose).
func wrapLines(s string, width int) []string {
	words := strings.Fields(strings.TrimSpace(s))
	if len(words) == 0 {
		return []string{"  "}
	}
	var out []string
	line := "  "
	for _, w := range words {
		if len(line)+len(w)+1 > width && strings.TrimSpace(line) != "" {
			out = append(out, line)
			line = "  "
		}
		if strings.TrimSpace(line) == "" {
			line += w
		} else {
			line += " " + w
		}
	}
	if strings.TrimSpace(line) != "" {
		out = append(out, line)
	}
	return out
}
