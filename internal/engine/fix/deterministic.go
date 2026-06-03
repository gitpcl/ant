// Package fix holds the Fixer adapters (TECHSPEC §5.2): deterministic (code
// transform, no LLM) and rawmodel (provider-agnostic OpenAI-compatible HTTP)
// in this sprint; the claudecode/codex/pi harness execs land in Sprint 009.
// Every adapter satisfies engine.Fixer with the uniform fix(task) -> diff shape
// and sets provenance (the Fixer string) on its ProposedDiff.
package fix

import (
	"context"
	"fmt"
	"strings"

	"github.com/gitpcl/ant/internal/engine"
)

// Transform names the deterministic code transforms (TECHSPEC §5.2, ADR-0002).
const (
	// TransformDeleteMatch deletes exactly the source line(s) the detector
	// matched. It backs the unused-import, dead-code, and unused-variable species
	// — mechanically-removable findings whose fix is "delete the matched span"
	// with no model judgment.
	TransformDeleteMatch = "delete-match"

	// TransformRewrite replaces the matched span's source line(s) with new text
	// taken from the detector rule's ast-grep `fix:` block (Finding.Replacement).
	// It backs species whose fix is a localized substitution rather than a
	// deletion (e.g. redundant-conversion: `int(x)` → `x`). The replacement is
	// spliced into the verbatim source line at the match's columns, so indentation
	// and surrounding code on the line are preserved. It is single-line only.
	TransformRewrite = "rewrite"

	// TransformReplaceMatch replaces the ENTIRE matched span — including a
	// MULTI-LINE span — with the detector rule's ast-grep `fix:` output
	// (Finding.Replacement). Unlike `rewrite` (a single-line, column-bounded
	// splice that preserves the rest of the line) and `delete-match` (a pure
	// deletion), it swaps the whole verbatim span (SourceLines) for the whole
	// replacement, so it can express a STRUCTURAL rewrite such as guard-clause
	// flattening (redundant-else: `if c { return } else { X }` → `if c { return }`
	// then `X`). The `-` lines are the verbatim source (they byte-match the working
	// tree); the `+` lines are the replacement verbatim. ast-grep does not re-indent
	// its `$$$`-spliced output, so the replacement's indentation may be imperfect;
	// that is intentionally left to the language's formatter, and the change is
	// proven semantics-preserving by the `compile` gate, not by cosmetic exactness.
	TransformReplaceMatch = "replace-match"
)

// supportedTransforms lists the transforms for the unknown-transform error so a
// misconfigured species sees every option it could have meant.
var supportedTransforms = strings.Join([]string{TransformDeleteMatch, TransformRewrite, TransformReplaceMatch}, ", ")

// deterministicFixer is a Fixer that applies a named code transform with NO
// network and NO model call (TECHSPEC §5.2). It derives the diff from the
// finding's span and the task's code context alone — no live model — but it does
// rely on the detector having captured the verbatim source line(s) (ast-grep
// `lines`, carried as CodeContext.SourceLines) so the patch byte-matches the
// working tree; it does not itself re-read the tree.
type deterministicFixer struct {
	transform string
}

// compile-time assertion that deterministicFixer satisfies engine.Fixer.
var _ engine.Fixer = (*deterministicFixer)(nil)

// NewDeterministic returns a deterministic Fixer for the named transform. An
// unknown transform is not rejected here — Fix returns the error, so a
// misconfigured species surfaces at fix time as a clean per-ant failure (which
// the colony turns into a skip) rather than a constructor panic.
func NewDeterministic(transform string) engine.Fixer {
	return &deterministicFixer{transform: transform}
}

// Fix produces a ProposedDiff that deletes (delete-match) or rewrites (rewrite)
// the finding's matched span. It never touches the network or a model. The diff
// is a standard unified-diff patch for the finding's file, so apply (go-git) and
// review consume it like any other adapter's output. Provenance is set to
// "deterministic (<transform>)".
//
// The line text to patch comes from the task's CodeContext — preferring
// SourceLines (the verbatim, indentation-preserving source line(s) the detector
// captured) so the `-`/` ` patch lines byte-match the working tree. The fixer
// does not re-read the working tree, keeping it consistent with the stateless
// one-task adapter contract (TECHSPEC §10).
func (f *deterministicFixer) Fix(_ context.Context, task engine.FixTask) (engine.ProposedDiff, error) {
	switch f.transform {
	case TransformDeleteMatch:
		return f.deleteMatch(task)
	case TransformRewrite:
		return f.rewrite(task)
	case TransformReplaceMatch:
		return f.replaceMatch(task)
	default:
		return engine.ProposedDiff{}, fmt.Errorf("fix: deterministic transform %q is not supported (known: %s)", f.transform, supportedTransforms)
	}
}

// deleteMatch builds a unified-diff patch that removes the source line(s) the
// finding spans. The removed `-` lines are the VERBATIM source line(s)
// (CodeContext.SourceLines, ast-grep `lines`) so they byte-match the working
// tree even when indented; the indentation-stripped Snippet is only a fallback
// for detectors that capture no full lines (the original column-0-only path).
// The hunk header uses the finding's 1-based start line and the line count.
func (f *deterministicFixer) deleteMatch(task engine.FixTask) (engine.ProposedDiff, error) {
	path, err := taskPath(task)
	if err != nil {
		return engine.ProposedDiff{}, err
	}

	text := matchedSource(task)
	if text == "" {
		return engine.ProposedDiff{}, fmt.Errorf("fix: delete-match needs the matched source line(s) on the finding/context for %s", path)
	}

	lines := splitLines(text)
	startLine := startLineOf(task)
	patch := unifiedDelete(path, startLine, lines)

	return engine.ProposedDiff{
		Files:     []engine.FileDiff{{Path: path, Patch: patch}},
		Fixer:     fmt.Sprintf("deterministic (%s)", f.transform),
		Rationale: fmt.Sprintf("delete-match removed %d matched line(s) at %s:%d (no model involved)", len(lines), path, startLine),
	}, nil
}

// rewrite builds a unified-diff patch that replaces the matched span's source
// line(s) with new text. The replacement (CodeContext.Replacement, ast-grep's
// `fix:` output) is spliced into the verbatim source line at the match's column
// range, so leading indentation and any code before/after the match on the line
// are preserved. The `-` lines byte-match the working tree (they are the
// verbatim source line(s)); the `+` line is the same line with the matched
// columns substituted.
//
// It handles the common single-line match. A multi-line span (the source line(s)
// contain a newline) is rejected with a clear error rather than risk a malformed
// splice — the verifier gate would skip such a fix anyway, and a multi-line
// rewrite source is out of scope for the deterministic transform.
func (f *deterministicFixer) rewrite(task engine.FixTask) (engine.ProposedDiff, error) {
	path, err := taskPath(task)
	if err != nil {
		return engine.ProposedDiff{}, err
	}

	src := task.Context.SourceLines
	if src == "" {
		src = task.Finding.SourceLines
	}
	if src == "" {
		return engine.ProposedDiff{}, fmt.Errorf("fix: rewrite needs the verbatim source line (SourceLines) on the finding/context for %s", path)
	}
	if strings.ContainsRune(strings.TrimRight(src, "\n"), '\n') {
		return engine.ProposedDiff{}, fmt.Errorf("fix: rewrite supports only a single-line match; got a multi-line span for %s", path)
	}

	replacement := task.Context.Replacement
	if replacement == "" {
		replacement = task.Finding.Replacement
	}
	if replacement == "" {
		return engine.ProposedDiff{}, fmt.Errorf("fix: rewrite needs a Replacement (the detector rule's ast-grep fix: output) for %s", path)
	}

	oldLine := strings.TrimRight(src, "\n")
	// Span columns are 1-based (engine.Span); convert to 0-based string offsets
	// into the single source line. EndCol is exclusive-end+1 in 1-based form, so
	// EndCol-1 is the 0-based exclusive end — exactly the slice boundary.
	startCol := task.Finding.Span.StartCol - 1
	endCol := task.Finding.Span.EndCol - 1
	if startCol < 0 || endCol < startCol || endCol > len(oldLine) {
		return engine.ProposedDiff{}, fmt.Errorf("fix: rewrite span [%d,%d) is out of range for the %d-char source line in %s", startCol, endCol, len(oldLine), path)
	}
	newLine := oldLine[:startCol] + replacement + oldLine[endCol:]

	startLine := startLineOf(task)
	patch := unifiedReplaceLine(path, startLine, oldLine, newLine)

	return engine.ProposedDiff{
		Files:     []engine.FileDiff{{Path: path, Patch: patch}},
		Fixer:     fmt.Sprintf("deterministic (%s)", f.transform),
		Rationale: fmt.Sprintf("rewrite replaced the matched span with %q at %s:%d (no model involved)", replacement, path, startLine),
	}, nil
}

// replaceMatch builds a unified-diff patch that replaces the ENTIRE matched span
// with the rule's `fix:` output. It deletes every verbatim source line
// (SourceLines, which byte-match the working tree) and adds every replacement
// line in their place — a multi-line N-old / M-new hunk. This is the structural
// transform behind redundant-else (guard-clause flattening): ast-grep matches
// the whole `if … else { … }`, its `fix:` re-emits the body without the else
// wrapper, and this transform swaps the span. Indentation in the replacement is
// left for the language formatter; the `compile` verifier proves the result is
// semantics-preserving (a flatten that broke control flow would fail to build).
func (f *deterministicFixer) replaceMatch(task engine.FixTask) (engine.ProposedDiff, error) {
	path, err := taskPath(task)
	if err != nil {
		return engine.ProposedDiff{}, err
	}

	src := task.Context.SourceLines
	if src == "" {
		src = task.Finding.SourceLines
	}
	if src == "" {
		return engine.ProposedDiff{}, fmt.Errorf("fix: replace-match needs the verbatim source line(s) (SourceLines) on the finding/context for %s", path)
	}

	replacement := task.Context.Replacement
	if replacement == "" {
		replacement = task.Finding.Replacement
	}
	if replacement == "" {
		return engine.ProposedDiff{}, fmt.Errorf("fix: replace-match needs a Replacement (the detector rule's ast-grep fix: output) for %s", path)
	}

	oldLines := splitLines(src)
	newLines := splitLines(replacement)
	startLine := startLineOf(task)
	patch := unifiedReplaceSpan(path, startLine, oldLines, newLines)

	return engine.ProposedDiff{
		Files:     []engine.FileDiff{{Path: path, Patch: patch}},
		Fixer:     fmt.Sprintf("deterministic (%s)", f.transform),
		Rationale: fmt.Sprintf("replace-match replaced a %d-line span with %d line(s) at %s:%d (no model involved)", len(oldLines), len(newLines), path, startLine),
	}, nil
}

// taskPath resolves the file path from the context, falling back to the finding.
func taskPath(task engine.FixTask) (string, error) {
	path := task.Context.File
	if path == "" {
		path = task.Finding.File
	}
	if path == "" {
		return "", fmt.Errorf("fix: deterministic transform needs a file path on the finding/context")
	}
	return path, nil
}

// matchedSource returns the verbatim source line(s) to operate on, preferring
// SourceLines (indentation preserved) and falling back to the Snippet so a
// detector that captures no full lines still works for column-0 deletions.
func matchedSource(task engine.FixTask) string {
	if s := task.Context.SourceLines; s != "" {
		return s
	}
	if s := task.Finding.SourceLines; s != "" {
		return s
	}
	if s := task.Context.Snippet; s != "" {
		return s
	}
	return task.Finding.Snippet
}

// startLineOf returns the finding's 1-based start line, clamped to a minimum of 1.
func startLineOf(task engine.FixTask) int {
	startLine := task.Finding.Span.StartLine
	if startLine < 1 {
		startLine = 1
	}
	return startLine
}

// splitLines splits a snippet into its constituent lines, dropping a single
// trailing empty element produced by a terminating newline so the deleted-line
// count matches the visible lines.
func splitLines(snippet string) []string {
	s := strings.ReplaceAll(snippet, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// unifiedDelete renders a minimal unified-diff patch that deletes count lines
// starting at startLine of path. The new-side line count is 0 (pure deletion).
func unifiedDelete(path string, startLine int, lines []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n", path)
	fmt.Fprintf(&b, "+++ b/%s\n", path)
	// Hunk: old range starts at startLine spanning len(lines); new range is the
	// same start with zero lines (a unified-diff pure deletion uses newStart =
	// oldStart for a non-empty old range).
	fmt.Fprintf(&b, "@@ -%d,%d +%d,0 @@\n", startLine, len(lines), startLine)
	for _, ln := range lines {
		fmt.Fprintf(&b, "-%s\n", ln)
	}
	return b.String()
}

// unifiedReplaceSpan renders a unified-diff patch that replaces the oldLines span
// (starting at startLine) with newLines — an N-old / M-new hunk. The `-` lines
// are the verbatim source lines so applyUnifiedPatch's exact-match check passes;
// the `+` lines are the replacement. Used by the replace-match transform for a
// multi-line structural rewrite (e.g. redundant-else flatten).
func unifiedReplaceSpan(path string, startLine int, oldLines, newLines []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n", path)
	fmt.Fprintf(&b, "+++ b/%s\n", path)
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", startLine, len(oldLines), startLine, len(newLines))
	for _, ln := range oldLines {
		fmt.Fprintf(&b, "-%s\n", ln)
	}
	for _, ln := range newLines {
		fmt.Fprintf(&b, "+%s\n", ln)
	}
	return b.String()
}

// unifiedReplaceLine renders a unified-diff patch that replaces the single line
// oldLine at startLine of path with newLine (a 1-old / 1-new hunk). The `-`
// line is the verbatim working-tree line so applyUnifiedPatch's exact-match
// check passes; the `+` line is the substituted line.
func unifiedReplaceLine(path string, startLine int, oldLine, newLine string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n", path)
	fmt.Fprintf(&b, "+++ b/%s\n", path)
	fmt.Fprintf(&b, "@@ -%d,1 +%d,1 @@\n", startLine, startLine)
	fmt.Fprintf(&b, "-%s\n", oldLine)
	fmt.Fprintf(&b, "+%s\n", newLine)
	return b.String()
}
