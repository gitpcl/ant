package testselect

import "github.com/gitpcl/ant/internal/engine/langmap"

// StrategyCoLocatedPy is the package-fallback-tier label for the Python
// co-located test selection (pytest).
const StrategyCoLocatedPy Strategy = "co-located (pytest)"

// NewPytest returns the Python co-located test selector for tests:affected
// (Sprint 026). For a changed `bar.py` it selects an existing co-located
// `test_bar.py` (or `bar_test.py`) in the same directory; the runner table runs
// `pytest <files…>` over exactly those. Package-fallback tier — scoped, never
// the whole suite.
func NewPytest() TestSelector {
	return &coLocatedSelector{
		language: langmap.Python,
		langOf:   langmap.LanguageForPath,
		testNames: func(srcBase string) []string {
			stem := stripExt(srcBase)
			return []string{
				"test_" + stem + ".py",
				stem + "_test.py",
			}
		},
		strategy: StrategyCoLocatedPy,
	}
}
