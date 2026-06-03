package verify

import (
	"strings"

	"github.com/gitpcl/ant/internal/engine"
)

// passResult builds a single-check VerifyResult marked passed. Every built-in
// verifier returns exactly one CheckResult naming its gate, so the colony's
// firstFailed/provenance logic always has a named check to surface.
func passResult(name, detail string) engine.VerifyResult {
	return engine.VerifyResult{
		Passed: true,
		Checks: []engine.CheckResult{{Name: name, Passed: true, Detail: detail}},
	}
}

// failResult builds a single-check VerifyResult marked failed, carrying the
// reason in detail so the skip is explainable (PRD §6.3 — never a silent drop).
func failResult(name, detail string) engine.VerifyResult {
	return engine.VerifyResult{
		Passed: false,
		Checks: []engine.CheckResult{{Name: name, Passed: false, Detail: detail}},
	}
}

// skipResult builds a single-check VerifyResult that PASSES the gate but records
// WHY the check did not actually run, so a diff in a language with no registered
// checker (or whose checker binary is absent) is a VISIBLE skip-with-reason
// rather than a vacuous green. The gate must not block on a skip — a missing
// checker is not the diff's fault — but the reason is always surfaced in
// CheckResult.Detail (the core Sprint 026 trust fix: honest skip, never a silent
// pass). The detail is prefixed "skipped: " so review/--json render it as a skip,
// distinct from a real pass.
func skipResult(name, reason string) engine.VerifyResult {
	return engine.VerifyResult{
		Passed: true,
		Checks: []engine.CheckResult{{Name: name, Passed: true, Detail: "skipped: " + reason}},
	}
}

// splitPatchLines splits a unified-diff patch into lines, normalizing CRLF and
// dropping a single trailing empty element from a terminating newline (mirrors
// fix.splitLines so the two sides agree on line counting).
func splitPatchLines(patch string) []string {
	s := strings.ReplaceAll(patch, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// hasPrefix is strings.HasPrefix, aliased so the verifier's line-classification
// reads as a small vocabulary rather than scattered stdlib calls.
func hasPrefix(s, prefix string) bool { return strings.HasPrefix(s, prefix) }
