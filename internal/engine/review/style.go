package review

import (
	"github.com/charmbracelet/lipgloss"
)

// styles holds the review TUI's semantic Lip Gloss styles (review-interaction.md
// §6). Tokens mirror the colony view's so the two screens are visually
// consistent. With color disabled (NO_COLOR / non-TTY) every style is the
// identity so output is plain text; glyphs + labels + diff +/- prefixes keep
// every distinction legible (§6 rule: never color-alone).
type styles struct {
	accepted lipgloss.Style // green  — ✔ accepted
	skipMark lipgloss.Style // amber  — ⊘ skipped (NOT red; a review-skip is not a failure)
	pending  lipgloss.Style // grey   — ◷ pending
	verified lipgloss.Style // green  — ✔ on passed verifiers
	diffAdd  lipgloss.Style // green  — + lines
	diffDel  lipgloss.Style // red    — - lines
	diffCtx  lipgloss.Style // grey   — context lines
	sevHigh  lipgloss.Style // red    — ● HIGH
	sevMed   lipgloss.Style // amber  — ● MEDIUM
	sevLow   lipgloss.Style // grey   — ● LOW
	chrome   lipgloss.Style // light grey borders/labels/verb bar
	warn     lipgloss.Style // amber  — confirm-quit / pending warnings
}

// newStyles builds the review palette. color=false yields all-identity styles.
func newStyles(color bool) styles {
	if !color {
		p := lipgloss.NewStyle()
		return styles{accepted: p, skipMark: p, pending: p, verified: p, diffAdd: p, diffDel: p,
			diffCtx: p, sevHigh: p, sevMed: p, sevLow: p, chrome: p, warn: p}
	}
	c := func(hex string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(hex)) }
	const (
		green = "#22C55E"
		red   = "#F87171"
		grey  = "#A1A1AA"
		amber = "#FBBF24"
		light = "#D4D4D8"
	)
	return styles{
		accepted: c(green),
		skipMark: c(amber),
		pending:  c(grey),
		verified: c(green),
		diffAdd:  c(green),
		diffDel:  c(red),
		diffCtx:  c(light),
		sevHigh:  c(red),
		sevMed:   c(amber),
		sevLow:   c(grey),
		chrome:   c(light),
		warn:     c(amber),
	}
}
