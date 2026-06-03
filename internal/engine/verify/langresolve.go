package verify

import (
	"errors"
	"os/exec"
	"sort"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/langmap"
)

// diffLanguages resolves the distinct, sorted set of canonical languages a diff
// touches, using the single langmap authority (Sprint 026). It is the shared
// language-resolution seam both the per-language compile and tests:affected
// verifiers dispatch on, so the two never disagree about what language a diff is.
//
// Files whose extension has no registered language resolve to langmap.Unknown,
// which is INCLUDED in the result so the caller can produce an honest
// "no checker for unknown" skip rather than silently ignoring them. The result
// is sorted for deterministic dispatch and reporting.
func diffLanguages(diff engine.ProposedDiff) []string {
	seen := make(map[string]bool)
	for _, fd := range diff.Files {
		seen[langmap.LanguageForPath(fd.Path)] = true
	}
	langs := make([]string, 0, len(seen))
	for l := range seen {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	return langs
}

// isBinaryNotFound reports whether err indicates the executable could not be
// located/started — the missing-binary case the per-language builders/runners
// must treat as a CLEAN SKIP (CI without php/node/tsc/pytest/phpunit stays
// green), mirroring detect.isBinaryNotFound and fix.isBinaryNotFound exactly.
func isBinaryNotFound(err error) bool {
	if err == nil {
		return false
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return true
	}
	return errors.Is(err, exec.ErrNotFound)
}
