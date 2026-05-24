package events

import (
	"os"

	"github.com/charmbracelet/lipgloss"
)

// Glyphs are the state icons. Unicode by default; an ASCII fallback set is used
// when --ascii is requested or a terminal cannot render the heavy marks
// (colony-view.md §6 rule 4, review-interaction.md §6). State is NEVER signaled
// by color alone — every state also carries one of these distinct glyphs plus a
// text label, so the view is legible in a monochrome terminal or to a colorblind
// user.
type Glyphs struct {
	Spinner  []string // animated braille frames (or "*" repeated in ASCII)
	Verified string   // ✔ / +
	Skipped  string   // ✖ / !
	Applied  string   // ↳ / ->
	Accepted string   // ✔ / +
	SkipMark string   // ⊘ / ~
	Pending  string   // ◷ / ?
	Warn     string   // ⚠ / !
	SevDot   string   // ● / *
}

// unicodeGlyphs is the default heavy-mark set (colony-view.md §3, §6).
var unicodeGlyphs = Glyphs{
	Spinner:  []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	Verified: "✔",
	Skipped:  "✖",
	Applied:  "↳",
	Accepted: "✔",
	SkipMark: "⊘",
	Pending:  "◷",
	Warn:     "⚠",
	SevDot:   "●",
}

// asciiGlyphs is the degraded fallback set (colony-view.md §6 rule 4,
// review-interaction.md §6): spinner→*, ✔→+, ✖→!, ↳→->, ●→*, ⊘→~, ◷→?, ⚠→!.
var asciiGlyphs = Glyphs{
	Spinner:  []string{"*"},
	Verified: "+",
	Skipped:  "!",
	Applied:  "->",
	Accepted: "+",
	SkipMark: "~",
	Pending:  "?",
	Warn:     "!",
	SevDot:   "*",
}

// GlyphSet returns the glyph set to use. ascii forces the ASCII fallback.
func GlyphSet(ascii bool) Glyphs {
	if ascii {
		return asciiGlyphs
	}
	return unicodeGlyphs
}

// palette holds the semantic Lip Gloss styles shared by the colony view and the
// review TUI (colony-view.md §6, review-interaction.md §6 — the two screens
// share tokens for visual consistency). Colors are dropped entirely when
// NO_COLOR is set or the output is not a TTY; the glyphs + labels above keep
// every state distinguishable with color off.
type palette struct {
	working  lipgloss.Style // cyan
	verified lipgloss.Style // green
	skipped  lipgloss.Style // red
	applied  lipgloss.Style // green (distinguished by the "↳ applied" label)
	queued   lipgloss.Style // grey
	chrome   lipgloss.Style // light grey borders/labels
	sevHigh  lipgloss.Style // red
	sevMed   lipgloss.Style // amber
	sevLow   lipgloss.Style // grey
	accepted lipgloss.Style // green
	skipMark lipgloss.Style // amber (deliberately NOT red — a review-skip is not a failure)
	pending  lipgloss.Style // grey
	warn     lipgloss.Style // amber
	diffAdd  lipgloss.Style // green
	diffDel  lipgloss.Style // red
	diffCtx  lipgloss.Style // light grey
}

// colorEnabled reports whether the renderer may emit ANSI color. Color is
// dropped when NO_COLOR is set (any value — the de-facto standard) per
// colony-view.md §6 rule 3 and review-interaction.md §6. The TUI program itself
// is only attached when stdout is a TTY (chosen by the CLI), so the non-TTY case
// is handled at renderer selection, not here.
func colorEnabled() bool {
	_, noColor := os.LookupEnv("NO_COLOR")
	return !noColor
}

// newPalette builds the shared style palette. When color is disabled every style
// is the identity (no ANSI), so output is plain text that still distinguishes
// states by glyph + label. Truecolor hex values come straight from the design
// tokens (colony-view.md §6 / review-interaction.md §6).
func newPalette(color bool) palette {
	if !color {
		plain := lipgloss.NewStyle()
		return palette{
			working: plain, verified: plain, skipped: plain, applied: plain,
			queued: plain, chrome: plain, sevHigh: plain, sevMed: plain, sevLow: plain,
			accepted: plain, skipMark: plain, pending: plain, warn: plain,
			diffAdd: plain, diffDel: plain, diffCtx: plain,
		}
	}
	c := func(hex string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(hex)) }
	const (
		cyan  = "#22D3EE"
		green = "#22C55E"
		red   = "#F87171"
		grey  = "#A1A1AA"
		amber = "#FBBF24"
		light = "#D4D4D8"
	)
	return palette{
		working:  c(cyan),
		verified: c(green),
		skipped:  c(red),
		applied:  c(green),
		queued:   c(grey),
		chrome:   c(light),
		sevHigh:  c(red),
		sevMed:   c(amber),
		sevLow:   c(grey),
		accepted: c(green),
		skipMark: c(amber),
		pending:  c(grey),
		warn:     c(amber),
		diffAdd:  c(green),
		diffDel:  c(red),
		diffCtx:  c(light),
	}
}
