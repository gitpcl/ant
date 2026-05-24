package events

import (
	"fmt"
	"strings"
)

// View renders the current model state. While the run is live it draws the four
// regions (colony-view.md §2); on run.end it switches to the static Summary
// (§4). View is a PURE projection of the model — it changes no state.
func (m *colonyModel) View() string {
	if m.done {
		return m.summaryView()
	}
	var b strings.Builder
	m.renderHeader(&b)
	b.WriteString("\n")
	m.renderLanes(&b)
	m.renderFailures(&b)
	m.renderFooter(&b)
	return b.String()
}

// renderHeader draws Region A (colony-view.md §2.1): the run identity + scope
// line and the species/queue/workers line.
func (m *colonyModel) renderHeader(b *strings.Builder) {
	indicator := m.pal.working.Render(m.spinner() + " working")
	if m.done {
		indicator = "done"
	}
	fmt.Fprintf(b, " ant fix · run %s · scanning %s%s%s\n",
		short(m.runID), orDot(m.scopeRoot), strings.Repeat(" ", 2), indicator)

	species := "all enabled"
	if len(m.species) > 0 {
		species = strings.Join(m.species, ", ")
	}
	fmt.Fprintf(b, " species: %s     queue %d · workers %d\n",
		species, m.queueDepth, m.workerCount)
}

// renderLanes draws Region B: one row per active worker lane, in first-seen
// order, capped at workerCount (colony-view.md §2, §7). Idle/collapsed lanes are
// skipped so the live swarm shows only what is happening now.
func (m *colonyModel) renderLanes(b *strings.Builder) {
	b.WriteString(" ANTS\n")
	shown := 0
	for _, id := range m.laneOrder {
		if shown >= m.workerCount {
			break
		}
		l := m.lanes[id]
		line := m.renderLane(l)
		if line == "" {
			continue
		}
		b.WriteString(line)
		shown++
		if l.applied != "" {
			fmt.Fprintf(b, "      %s\n", m.pal.applied.Render(m.glyphs.Applied+" applied → "+l.applied))
		}
	}
	if shown == 0 {
		b.WriteString("  (waiting for ants…)\n")
	}
}

// renderLane renders one lane per its state (colony-view.md §3). Returns "" for
// an idle/collapsed lane (a freed worker slot, not yet reused).
func (m *colonyModel) renderLane(l *lane) string {
	switch l.state {
	case stateWorking:
		return fmt.Sprintf("  %s #%d  %s  %-13s %s  fixing…  %.1fs\n",
			m.pal.working.Render(m.spinner()), l.antID, m.pal.working.Render("WORKING "),
			truncSpecies(l.species), m.fileLine(l.file, l.startLine), m.elapsed(l))
	case stateVerified:
		return fmt.Sprintf("  %s #%d  %s  %-13s %s  staged\n",
			m.pal.verified.Render(m.glyphs.Verified), l.antID, m.pal.verified.Render("VERIFIED"),
			truncSpecies(l.species), m.fileLine(l.file, l.startLine))
	case stateSkipped:
		return fmt.Sprintf("  %s #%d  %s  %-13s %s  %s failed\n",
			m.pal.skipped.Render(m.glyphs.Skipped), l.antID, m.pal.skipped.Render("SKIPPED "),
			truncSpecies(l.species), m.fileLine(l.file, l.startLine), l.failCheck)
	default:
		return ""
	}
}

// renderFailures draws Region C, the persistent Failures panel. It is hidden
// until the first skip, then stays for the rest of the run (colony-view.md
// §3.3). At most panelMax rows are shown with a trailing "+k more" note; the
// full list is reprinted on the Summary screen.
func (m *colonyModel) renderFailures(b *strings.Builder) {
	if len(m.failures) == 0 {
		return
	}
	b.WriteString("\n")
	header := fmt.Sprintf("%s FAILURES (%d)  — skipped, NOT applied", m.glyphs.Warn, len(m.failures))
	fmt.Fprintf(b, " %s\n", m.pal.skipped.Render(header))

	const panelMax = 8
	rows := m.failures
	hidden := 0
	if len(rows) > panelMax {
		hidden = len(rows) - panelMax
		rows = rows[len(rows)-panelMax:] // most recent panelMax
	}
	for _, f := range rows {
		fmt.Fprintf(b, "  %s %s  %s   %s: %q\n",
			m.pal.skipped.Render(m.glyphs.Skipped), f.species,
			fileLineStr(f.file, f.line), f.check, f.detail)
	}
	if hidden > 0 {
		fmt.Fprintf(b, "  … +%d more (see summary)\n", hidden)
	}
}

// renderFooter draws Region D: the divider, the counter line, the progress bar,
// and the quit hint (colony-view.md §2.1). Counters are the live running tallies;
// run.end is authoritative for the Summary, not for the live footer.
func (m *colonyModel) renderFooter(b *strings.Builder) {
	b.WriteString(" " + m.pal.chrome.Render(strings.Repeat("─", 73)) + "\n")

	highest := strings.ToUpper(m.highestSev.String())
	fmt.Fprintf(b, " found %d   %s verified %d   %s skipped %d   ◷ in flight %d        highest: %s\n",
		m.found,
		m.pal.verified.Render(m.glyphs.Verified), m.verified,
		m.pal.skipped.Render(m.glyphs.Skipped), m.skipped,
		m.inFlight, m.styleSeverity(highest))

	resolved := m.verified + m.skipped
	b.WriteString(" " + progressBar(resolved, m.found, 28) + fmt.Sprintf(" %d/%d resolved\n", resolved, m.found))
	b.WriteString(" q quit   (nothing is applied — verified diffs are staged for `ant review`)\n")
}

// summaryView renders the end-of-run static Summary (colony-view.md §4). Counts
// come DIRECTLY from RunEnd (authoritative), not the running tallies. An aborted
// run (RunEnd.Error set) shows the abort screen instead (§4.3).
func (m *colonyModel) summaryView() string {
	if m.end.Error != "" {
		return m.abortedView()
	}
	var b strings.Builder
	fmt.Fprintf(&b, " ant fix · run %s · %s — done\n\n", short(m.end.RunID), orDot(m.scopeRoot))

	box := []string{
		fmt.Sprintf("  found        %d", m.end.Findings),
		fmt.Sprintf("  %s verified   %d   (staged for review)", m.pal.verified.Render(m.glyphs.Verified), m.end.Verified),
		fmt.Sprintf("  %s skipped    %d%s", m.pal.skipped.Render(m.glyphs.Skipped), m.end.Skipped, skippedNote(m.end.Skipped)),
		fmt.Sprintf("  highest severity   %s", m.styleSeverity(strings.ToUpper(m.end.HighestSeverity))),
	}
	b.WriteString(m.titledBox("COLONY SUMMARY", box))
	b.WriteString("\n")

	if m.end.Skipped > 0 {
		b.WriteString("\n")
		fmt.Fprintf(&b, " %s FAILURES (%d)  — these fixes were skipped and never applied\n",
			m.pal.skipped.Render(m.glyphs.Warn), len(m.failures))
		for _, f := range m.failures {
			fmt.Fprintf(&b, "  %s %s  %s   %s: %q\n",
				m.pal.skipped.Render(m.glyphs.Skipped), f.species,
				fileLineStr(f.file, f.line), f.check, f.detail)
		}
	}

	b.WriteString("\n")
	if m.end.Applied > 0 {
		fmt.Fprintf(&b, " %d diffs applied.\n", m.end.Applied)
	} else {
		if m.end.Verified > 0 {
			fmt.Fprintf(&b, " %d verified diffs are staged. Run `ant review` to walk them.\n", m.end.Verified)
		}
		b.WriteString(" Nothing was applied (run without --apply).\n")
	}
	return b.String()
}

// abortedView renders the operational-error summary (colony-view.md §4.3),
// mirroring the human renderer: no counts, just the verbatim error.
func (m *colonyModel) abortedView() string {
	var b strings.Builder
	fmt.Fprintf(&b, " ant fix · run %s — ABORTED\n\n", short(m.end.RunID))
	fmt.Fprintf(&b, " %s run failed: %s\n\n", m.pal.skipped.Render(m.glyphs.Skipped), m.end.Error)
	b.WriteString(" Partial results were discarded. Nothing was staged or applied.\n")
	return b.String()
}

// --- small rendering helpers --------------------------------------------------

// spinner returns the current braille frame (colony-view.md §3.1).
func (m *colonyModel) spinner() string {
	frames := m.glyphs.Spinner
	return frames[m.spinnerFrame%len(frames)]
}

// elapsed is seconds since the lane's ant.start, the renderer-local progress
// timer (colony-view.md §3.1).
func (m *colonyModel) elapsed(l *lane) float64 {
	if l.startedAt.IsZero() {
		return 0
	}
	return m.now.Sub(l.startedAt).Seconds()
}

// fileLine renders "path:line" with left-truncation so the filename + line stay
// visible (colony-view.md §7).
func (m *colonyModel) fileLine(file string, line int) string {
	return truncPathLeft(fileLineStr(file, line), 30)
}

// styleSeverity colors the uppercase severity word per the severity tokens
// (colony-view.md §6). The word itself disambiguates (never color-alone).
func (m *colonyModel) styleSeverity(word string) string {
	switch word {
	case "HIGH":
		return m.pal.sevHigh.Render(word)
	case "MEDIUM":
		return m.pal.sevMed.Render(word)
	case "LOW":
		return m.pal.sevLow.Render(word)
	default:
		return m.pal.queued.Render(word)
	}
}

// titledBox draws a bordered box with a centered title (colony-view.md §4 box).
func (m *colonyModel) titledBox(title string, lines []string) string {
	const width = 74
	var b strings.Builder
	dash := (width - len(title) - 2) / 2
	top := fmt.Sprintf(" ╭─%s %s %s╮", strings.Repeat("─", dash), title, strings.Repeat("─", width-len(title)-dash-3))
	b.WriteString(m.pal.chrome.Render(top) + "\n")
	for _, ln := range lines {
		b.WriteString(m.pal.chrome.Render(" │") + ln + "\n")
	}
	b.WriteString(m.pal.chrome.Render(" ╰"+strings.Repeat("─", width)+"╯") + "\n")
	return b.String()
}

// fileLineStr formats a file path with its 1-based line, omitting the line when
// it is unset.
func fileLineStr(file string, line int) string {
	if line > 0 {
		return fmt.Sprintf("%s:%d", file, line)
	}
	return file
}

// short returns the first 6 runes of an id (e.g. a run id) for the compact
// header (colony-view.md §0). Shorter ids pass through unchanged.
func short(s string) string {
	if len(s) <= 6 {
		return s
	}
	return s[:6]
}

// orDot returns "." for an empty scope root so the header always names a target.
func orDot(root string) string {
	if root == "" {
		return "."
	}
	return root
}

// truncSpecies pads/truncates a species name to the fixed 13-col lane field
// (colony-view.md §3 line templates).
func truncSpecies(s string) string {
	const w = 13
	if len(s) > w {
		return s[:w]
	}
	return s
}

// truncPathLeft truncates a string from the LEFT with a leading "…" so the tail
// (filename + line) stays visible (colony-view.md §7). max <= 1 returns s.
func truncPathLeft(s string, max int) string {
	if max <= 1 || len(s) <= max {
		return s
	}
	return "…" + s[len(s)-(max-1):]
}

// progressBar renders an N-wide filled/empty bar for done/total (colony-view.md
// §2.1). A zero total renders an empty bar (no division by zero).
func progressBar(done, total, width int) string {
	if width < 1 {
		width = 1
	}
	filled := 0
	if total > 0 {
		filled = done * width / total
		if filled > width {
			filled = width
		}
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}

// skippedNote appends the "(NOT applied — see below)" caveat only when there are
// skips (colony-view.md §4.1).
func skippedNote(skipped int) string {
	if skipped > 0 {
		return "   (NOT applied — see below)"
	}
	return ""
}
