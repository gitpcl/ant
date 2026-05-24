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
