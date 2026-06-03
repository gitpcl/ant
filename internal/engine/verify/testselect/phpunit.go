package testselect

import "github.com/gitpcl/ant/internal/engine/langmap"

// StrategyCoLocatedPHP is the package-fallback-tier label for the PHP co-located
// test selection (phpunit).
const StrategyCoLocatedPHP Strategy = "co-located (phpunit)"

// NewPHPUnit returns the PHP co-located test selector for tests:affected
// (Sprint 026). For a changed `Baz.php` it selects an existing co-located
// `BazTest.php` in the same directory; the runner table runs `phpunit <files…>`
// over exactly those. Package-fallback tier — scoped, never the whole suite.
func NewPHPUnit() TestSelector {
	return &coLocatedSelector{
		language: langmap.PHP,
		langOf:   langmap.LanguageForPath,
		testNames: func(srcBase string) []string {
			stem := stripExt(srcBase)
			return []string{stem + "Test.php"}
		},
		strategy: StrategyCoLocatedPHP,
	}
}
