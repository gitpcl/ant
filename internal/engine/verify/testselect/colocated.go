package testselect

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gitpcl/ant/internal/engine"
)

// coLocatedSelector is the package-fallback-tier selector for the non-Go
// language waves (Sprint 026): given a changed source file, it finds the
// co-located test file(s) for that language and selects them. It is the
// language-specific analogue of packageFallbackSelector (which is Go/package
// scoped) — coarse but still SCOPED to the changed file's own test(s), NEVER a
// whole-suite run.
//
// The mapping from a source file to its test file is language-specific and
// supplied by testNames: e.g. for TypeScript, `foo.ts` → its sibling
// `foo.test.ts`/`foo.spec.ts`; for Python `bar.py` → `test_bar.py`; for PHP
// `Baz.php` → `BazTest.php`. The selector resolves candidates relative to the
// scope root (the post-fix scratch tree) and only selects test files that
// actually EXIST there, so a change with no co-located test declines (OK=false)
// and the verifier reports an honest "no affected tests" rather than fabricating
// a run.
type coLocatedSelector struct {
	// language is the canonical token this selector applies to (its Select
	// returns OK=false for changes whose file is not that language, so the
	// verifier's language dispatch stays simple).
	language string
	// langOf resolves a path to its canonical language (injected so the package
	// has no import cycle with langmap; the verifier wires langmap.LanguageForPath).
	langOf func(path string) string
	// testNames maps a changed source file's base name to the candidate test file
	// base names to look for in the same directory.
	testNames func(srcBase string) []string
	// strategy is the label reported in provenance (always package-fallback-tier
	// coarseness, but named per language for clarity).
	strategy Strategy
}

// compile-time assertion.
var _ TestSelector = (*coLocatedSelector)(nil)

// Select maps each changed file of this selector's language to its existing
// co-located test file(s) within the scope root, de-duplicated and sorted.
// Returns OK=false when no changed file is this language or none has a co-located
// test on disk — so the verifier degrades honestly instead of inventing a run.
func (s *coLocatedSelector) Select(_ context.Context, changes []Change, scope engine.Scope) (Selection, error) {
	root := scope.Root
	if root == "" {
		root = "."
	}
	seen := make(map[string]bool)
	var tests []string
	for _, ch := range changes {
		if s.langOf(ch.File) != s.language {
			continue
		}
		dir := filepath.Dir(filepath.ToSlash(ch.File))
		base := filepath.Base(ch.File)
		for _, cand := range s.testNames(base) {
			rel := cand
			if dir != "." && dir != "" {
				rel = dir + "/" + cand
			}
			if seen[rel] {
				continue
			}
			abs := filepath.Join(root, filepath.FromSlash(rel))
			if fileExists(abs) {
				seen[rel] = true
				tests = append(tests, rel)
			}
		}
		// A changed file that IS itself a test file is its own affected test.
		if isTestFileName(base, s.testNames) {
			rel := filepath.ToSlash(ch.File)
			if !seen[rel] && fileExists(filepath.Join(root, filepath.FromSlash(rel))) {
				seen[rel] = true
				tests = append(tests, rel)
			}
		}
	}
	if len(tests) == 0 {
		return Selection{Strategy: s.strategy}, nil
	}
	sort.Strings(tests)
	return Selection{
		Tests:    tests,
		Packages: tests, // the runner receives the test file paths as its positional args
		Strategy: s.strategy,
		OK:       true,
	}, nil
}

// isTestFileName reports whether base is itself a test file for the language,
// detected by checking whether mapping the stripped source name back through
// testNames would produce base. A pragmatic heuristic shared across languages:
// a *.test.ts / *.spec.ts, test_*.py, or *Test.php file is a test file.
func isTestFileName(base string, _ func(string) []string) bool {
	switch {
	case strings.HasSuffix(base, ".test.ts"), strings.HasSuffix(base, ".test.tsx"),
		strings.HasSuffix(base, ".spec.ts"), strings.HasSuffix(base, ".spec.tsx"),
		strings.HasSuffix(base, ".test.js"), strings.HasSuffix(base, ".spec.js"):
		return true
	case strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"):
		return true
	case strings.HasSuffix(base, "Test.php"):
		return true
	}
	return false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
