package verify

import (
	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/verify/testselect"
)

// changesFromDiff extracts the per-file changed lines from a ProposedDiff for the
// test selectors. For each FileDiff it walks the unified-diff hunks and records
// the NEW-file line numbers of every added (`+`) line, plus the line at each
// removed (`-`) line's position (so a deletion still points the selector at the
// region it touched). Line numbers are 1-based to match coverage profiles and
// editor conventions.
//
// It reuses the same hunk-header parser the scratch-tree apply uses (scratch.go),
// so the two agree on line counting and there is no second diff dialect to
// maintain. A patch with no parseable hunks yields a Change with the file path and
// no lines — the import-graph / package-fallback selectors still work at file
// granularity, only coverage-map needs the lines.
func changesFromDiff(diff engine.ProposedDiff) []testselect.Change {
	changes := make([]testselect.Change, 0, len(diff.Files))
	for _, fd := range diff.Files {
		changes = append(changes, testselect.Change{
			File:  fd.Path,
			Lines: changedLinesOf(fd.Patch),
		})
	}
	return changes
}

// changedLinesOf returns the NEW-file 1-based line numbers touched by a unified
// patch. Within each hunk a running cursor tracks the new-file line; a `+` or ` `
// line advances it, a `+` line is recorded as changed, and a `-` line records the
// current cursor position (the spot the removal affects) without advancing it.
func changedLinesOf(patch string) []int {
	var lines []int
	newLine := 0
	for _, ln := range splitPatchLines(patch) {
		switch {
		case hasPrefix(ln, "+++"), hasPrefix(ln, "---"):
			continue
		case hasPrefix(ln, "@@"):
			h, err := parseHunkHeader(ln)
			if err != nil {
				continue
			}
			newLine = h.newStart
		case hasPrefix(ln, "+"):
			if newLine > 0 {
				lines = append(lines, newLine)
				newLine++
			}
		case hasPrefix(ln, "-"):
			if newLine > 0 {
				lines = append(lines, newLine) // removal site in new-file coordinates
			}
		case hasPrefix(ln, " "):
			if newLine > 0 {
				newLine++
			}
		}
	}
	return lines
}
