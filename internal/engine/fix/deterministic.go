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
	// TransformDeleteMatch deletes exactly the lines the detector matched. It
	// backs the unused-import and dead-code species — mechanically-removable
	// findings whose fix is "delete the matched span" with no model judgment.
	TransformDeleteMatch = "delete-match"
)

// deterministicFixer is a Fixer that applies a named code transform with NO
// network and NO model call (TECHSPEC §5.2). It is pure: given a FixTask it
// derives the diff from the finding's span and the task's code context alone.
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

// Fix produces a ProposedDiff that deletes the finding's matched span. It never
// touches the network or a model. The diff is a standard unified-diff patch for
// the finding's file, so apply (go-git) and review consume it like any other
// adapter's output. Provenance is set to "deterministic (<transform>)".
//
// The line text to delete comes from the task's CodeContext (the snippet the
// detector captured) — the deterministic fixer does not re-read the working
// tree, keeping it pure and testable and consistent with the stateless
// one-task adapter contract (TECHSPEC §10).
func (f *deterministicFixer) Fix(_ context.Context, task engine.FixTask) (engine.ProposedDiff, error) {
	switch f.transform {
	case TransformDeleteMatch:
		return f.deleteMatch(task)
	default:
		return engine.ProposedDiff{}, fmt.Errorf("fix: deterministic transform %q is not supported (known: %s)", f.transform, TransformDeleteMatch)
	}
}

// deleteMatch builds a unified-diff patch that removes the lines spanned by the
// finding. The deleted text is the finding/context snippet; the hunk header
// uses the finding's 1-based start line and the number of lines removed.
func (f *deterministicFixer) deleteMatch(task engine.FixTask) (engine.ProposedDiff, error) {
	path := task.Context.File
	if path == "" {
		path = task.Finding.File
	}
	if path == "" {
		return engine.ProposedDiff{}, fmt.Errorf("fix: delete-match needs a file path on the finding/context")
	}

	snippet := task.Context.Snippet
	if snippet == "" {
		snippet = task.Finding.Snippet
	}
	if snippet == "" {
		return engine.ProposedDiff{}, fmt.Errorf("fix: delete-match needs the matched snippet on the finding/context for %s", path)
	}

	lines := splitLines(snippet)
	startLine := task.Finding.Span.StartLine
	if startLine < 1 {
		startLine = 1
	}

	patch := unifiedDelete(path, startLine, lines)

	return engine.ProposedDiff{
		Files:     []engine.FileDiff{{Path: path, Patch: patch}},
		Fixer:     fmt.Sprintf("deterministic (%s)", f.transform),
		Rationale: fmt.Sprintf("delete-match removed %d matched line(s) at %s:%d (no model involved)", len(lines), path, startLine),
	}, nil
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
