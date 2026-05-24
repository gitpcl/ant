// Package apply lands accepted/trusted staged diffs into the working tree using
// go-git IN-PROCESS (TECHSPEC §2 — no dependency on a `git` binary). It is the
// only package that mutates source files, and it does so only for diffs a
// reviewer accepted (`ant apply`) or a trusted species auto-applied (`ant fix
// --apply`). Branch-by-default keeps the change reviewable; --no-branch lands on
// the current branch. patch.go owns the pure unified-diff application; apply.go
// owns the go-git branch/commit landing.
package apply

import (
	"fmt"
	"strconv"
	"strings"
)

// applyUnifiedPatch applies a single-file unified-diff patch to src and returns
// the patched content. It is a pure function (no I/O) so it is fully unit
// testable and shared by both the real file landing and the tests.
//
// It supports the standard unified-diff body the Ant fixers emit: ---/+++ file
// headers, @@ -a,b +c,d @@ hunk headers, and context( ) / removed(-) / added(+)
// lines. It applies hunks by their old-side start line, verifying that the
// context/removed lines match the source so a stale or malformed patch fails
// loudly rather than corrupting the file (a wrong apply is exactly what the
// verifier gate exists to prevent — never silently mangle code).
func applyUnifiedPatch(src, patch string) (string, error) {
	hunks, err := parseHunks(patch)
	if err != nil {
		return "", err
	}
	if len(hunks) == 0 {
		return src, nil // nothing to change (e.g. headers only)
	}

	srcLines := splitKeepEnding(src)
	var out []string
	cursor := 0 // 0-based index into srcLines, lines already copied through

	for _, h := range hunks {
		// Copy untouched lines before this hunk's old start (1-based → 0-based).
		start := h.oldStart - 1
		if start < cursor {
			return "", fmt.Errorf("apply: overlapping or out-of-order hunk at old line %d", h.oldStart)
		}
		if start > len(srcLines) {
			return "", fmt.Errorf("apply: hunk old start %d is beyond end of file (%d lines)", h.oldStart, len(srcLines))
		}
		out = append(out, srcLines[cursor:start]...)
		cursor = start

		for _, ln := range h.lines {
			switch ln.kind {
			case ctxLine:
				if cursor >= len(srcLines) || stripEnding(srcLines[cursor]) != ln.text {
					return "", fmt.Errorf("apply: context mismatch at old line %d: patch expected %q", cursor+1, ln.text)
				}
				out = append(out, srcLines[cursor])
				cursor++
			case delLine:
				if cursor >= len(srcLines) || stripEnding(srcLines[cursor]) != ln.text {
					return "", fmt.Errorf("apply: cannot delete at old line %d: patch expected %q, file has %q", cursor+1, ln.text, peek(srcLines, cursor))
				}
				cursor++ // drop the source line (not appended to out)
			case addLine:
				out = append(out, ln.text+"\n") // added lines carry a newline
			}
		}
	}
	// Copy any remaining lines after the last hunk.
	out = append(out, srcLines[cursor:]...)

	joined := strings.Join(out, "")
	// Preserve the source's trailing-newline disposition when the patch added a
	// final line that we newline-terminated but the file did not end in newline.
	return joined, nil
}

// lineKind classifies a unified-diff body line.
type lineKind int

const (
	ctxLine lineKind = iota // " " context (unchanged)
	delLine                 // "-" removed
	addLine                 // "+" added
)

// patchLine is one classified body line; text is the line WITHOUT its leading
// +/-/space marker and WITHOUT a trailing newline (the applier re-adds endings).
type patchLine struct {
	kind lineKind
	text string
}

// hunk is one @@ -oldStart,oldLen +newStart,newLen @@ block and its body lines.
type hunk struct {
	oldStart int
	lines    []patchLine
}

// parseHunks extracts the hunks from a unified-diff patch, ignoring the ---/+++
// file headers (a patch targets a single known file here). A line that is not a
// header, a hunk header, or a +/-/space body line is rejected so a malformed
// patch fails loudly.
func parseHunks(patch string) ([]hunk, error) {
	var hunks []hunk
	var cur *hunk

	for _, raw := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(raw, "--- "), strings.HasPrefix(raw, "+++ "):
			continue // file headers — not needed (single target file)
		case strings.HasPrefix(raw, "@@"):
			oldStart, err := parseHunkHeader(raw)
			if err != nil {
				return nil, err
			}
			hunks = append(hunks, hunk{oldStart: oldStart})
			cur = &hunks[len(hunks)-1]
		case raw == "":
			// A trailing empty token from the final newline; ignore it.
			continue
		default:
			if cur == nil {
				return nil, fmt.Errorf("apply: patch body line before any @@ hunk header: %q", raw)
			}
			marker, body := raw[0], raw[1:]
			switch marker {
			case ' ':
				cur.lines = append(cur.lines, patchLine{kind: ctxLine, text: body})
			case '-':
				cur.lines = append(cur.lines, patchLine{kind: delLine, text: body})
			case '+':
				cur.lines = append(cur.lines, patchLine{kind: addLine, text: body})
			case '\\':
				continue // "\ No newline at end of file" marker — ignore
			default:
				return nil, fmt.Errorf("apply: unexpected patch line %q (want space/+/-)", raw)
			}
		}
	}
	return hunks, nil
}

// parseHunkHeader reads the old-side start line from "@@ -a,b +c,d @@ ...". Only
// the old start is needed to place the hunk; lengths are validated implicitly by
// the context/delete matching during application.
func parseHunkHeader(line string) (int, error) {
	// Format: @@ -oldStart[,oldLen] +newStart[,newLen] @@ optional-section
	fields := strings.Fields(line)
	if len(fields) < 2 || !strings.HasPrefix(fields[1], "-") {
		return 0, fmt.Errorf("apply: malformed hunk header %q", line)
	}
	oldSpec := strings.TrimPrefix(fields[1], "-")
	if i := strings.IndexByte(oldSpec, ','); i >= 0 {
		oldSpec = oldSpec[:i]
	}
	n, err := strconv.Atoi(oldSpec)
	if err != nil {
		return 0, fmt.Errorf("apply: bad hunk old-start in %q: %v", line, err)
	}
	if n < 1 {
		n = 1
	}
	return n, nil
}

// splitKeepEnding splits content into lines WITH their trailing "\n" preserved,
// so reconstruction is byte-faithful. A final line without a newline is kept as
// its own element without one.
func splitKeepEnding(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:]) // last line, no trailing newline
	}
	return lines
}

// stripEnding removes a single trailing "\n" (and a preceding "\r") so a body
// line compares equal to a patch line regardless of line-ending style.
func stripEnding(s string) string {
	s = strings.TrimSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\r")
	return s
}

// peek returns the stripped source line at i for error messages, or "<EOF>".
func peek(lines []string, i int) string {
	if i < 0 || i >= len(lines) {
		return "<EOF>"
	}
	return stripEnding(lines[i])
}
