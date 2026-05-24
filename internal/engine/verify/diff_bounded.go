// Package verify holds the built-in Verifier runners (TECHSPEC §5.3) and the
// machinery the colony uses to gate fixes: diff-bounded (a pure size guard),
// compile (apply to a scratch tree and build there), detector-clears (re-run the
// owning detector and assert the finding is gone), and a Chain that runs them in
// the required order (diff-bounded FIRST — TECHSPEC §8.1).
//
// Every runner satisfies engine.Verifier and returns a VerifyResult whose Checks
// carry name + pass/fail + detail for provenance, so a skip is always explainable
// (PRD §6.3 — a skip is a trust signal, never a hidden error). The I/O verifiers
// apply the proposed diff to a SCRATCH COPY of the tree and run their check there;
// the real working tree is never mutated.
package verify

import (
	"context"
	"fmt"

	"github.com/gitpcl/ant/internal/engine"
)

// CheckDiffBounded is the canonical name of the diff-bounded check, recorded in
// its CheckResult so review/--json can show which gate fired.
const CheckDiffBounded = "diff-bounded"

// Default size limits for diff-bounded. They guard runaway edits — a fixer that
// rewrites an entire file instead of making a localized change is exactly the
// failure this verifier catches early (TECHSPEC §5.3, §8.1). The defaults are
// deliberately generous: legitimate localized fixes stay well under them, while a
// runaway rewrite blows past. Configurable via Limits / NewDiffBounded options
// (the colony wires the resolved ant.toml value; see config.DefaultDiff*).
const (
	// DefaultMaxChangedLines caps total added+removed diff lines across all files.
	DefaultMaxChangedLines = 200
	// DefaultMaxChangedFiles caps how many files a single fix may touch.
	DefaultMaxChangedFiles = 10
)

// Limits parameterizes diff-bounded. A zero or negative field means "unbounded"
// for that dimension, so a caller can guard on lines only, files only, or both.
type Limits struct {
	MaxChangedLines int
	MaxChangedFiles int
}

// DefaultLimits returns the built-in diff-bounded limits.
func DefaultLimits() Limits {
	return Limits{
		MaxChangedLines: DefaultMaxChangedLines,
		MaxChangedFiles: DefaultMaxChangedFiles,
	}
}

// diffBoundedVerifier rejects a ProposedDiff whose size exceeds the configured
// limits. It is a PURE function verifier: it inspects the diff's patch text only,
// touches no I/O, and so is the cheapest gate — which is why the colony runs it
// FIRST, killing runaway edits before the expensive compile/detector-clears runs
// (TECHSPEC §8.1).
type diffBoundedVerifier struct {
	limits Limits
}

// compile-time assertion that diffBoundedVerifier satisfies engine.Verifier.
var _ engine.Verifier = (*diffBoundedVerifier)(nil)

// NewDiffBounded returns a diff-bounded verifier with the given limits. A
// zero-value Limits means unbounded on both dimensions (a no-op gate); callers
// wanting the built-in caps pass DefaultLimits().
func NewDiffBounded(limits Limits) engine.Verifier {
	return &diffBoundedVerifier{limits: limits}
}

// Verify counts the changed lines and files in the diff and fails if either
// exceeds its configured limit. The scope is unused — the check depends only on
// the diff. The returned CheckResult names the diff-bounded gate and, on failure,
// states which limit was exceeded and by how much, so the skip reason is concrete.
func (v *diffBoundedVerifier) Verify(_ context.Context, diff engine.ProposedDiff, _ engine.Scope) engine.VerifyResult {
	files := len(diff.Files)
	lines := countChangedLines(diff)

	if v.limits.MaxChangedFiles > 0 && files > v.limits.MaxChangedFiles {
		return failResult(CheckDiffBounded, fmt.Sprintf(
			"diff touches %d files, exceeding the limit of %d (guards runaway edits)",
			files, v.limits.MaxChangedFiles))
	}
	if v.limits.MaxChangedLines > 0 && lines > v.limits.MaxChangedLines {
		return failResult(CheckDiffBounded, fmt.Sprintf(
			"diff changes %d lines, exceeding the limit of %d (guards runaway edits)",
			lines, v.limits.MaxChangedLines))
	}

	return passResult(CheckDiffBounded, fmt.Sprintf(
		"diff within bounds: %d file(s), %d changed line(s)", files, lines))
}

// countChangedLines totals the added (+) and removed (-) lines across every file
// patch, ignoring unified-diff metadata lines (---, +++, @@). It counts the
// change magnitude, not net delta, so a large rewrite (many - and many +) is
// fully accounted for.
func countChangedLines(diff engine.ProposedDiff) int {
	total := 0
	for _, fd := range diff.Files {
		total += countPatchChangedLines(fd.Patch)
	}
	return total
}

// countPatchChangedLines counts +/- body lines in one unified-diff patch,
// skipping the file headers (---/+++) and hunk headers (@@). A leading +/- on a
// header line is excluded by checking the header prefixes first.
func countPatchChangedLines(patch string) int {
	n := 0
	for _, ln := range splitPatchLines(patch) {
		switch {
		case hasPrefix(ln, "+++"), hasPrefix(ln, "---"), hasPrefix(ln, "@@"):
			continue
		case hasPrefix(ln, "+"), hasPrefix(ln, "-"):
			n++
		}
	}
	return n
}
