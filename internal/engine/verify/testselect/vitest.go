package testselect

import "github.com/gitpcl/ant/internal/engine/langmap"

// StrategyCoLocatedTS is the package-fallback-tier label for the TypeScript /
// JavaScript co-located test selection (vitest). It is reported in provenance so
// the developer sees the check was coarse (co-located file), not a precise
// coverage-mapped selection.
const StrategyCoLocatedTS Strategy = "co-located (vitest)"

// NewVitest returns the TypeScript/JavaScript co-located test selector for the
// tests:affected verifier (Sprint 026). For a changed `foo.ts` it selects an
// existing sibling `foo.test.ts` / `foo.spec.ts` (and the .tsx/.js/.jsx forms);
// the runner table runs `vitest run <files…>` over exactly those. It is the
// package-fallback tier — coarse but scoped — never a whole-suite run.
func NewVitest() TestSelector {
	return &coLocatedSelector{
		language: langmap.TypeScript,
		langOf:   langmap.LanguageForPath,
		testNames: func(srcBase string) []string {
			stem := stripExt(srcBase)
			return []string{
				stem + ".test.ts", stem + ".spec.ts",
				stem + ".test.tsx", stem + ".spec.tsx",
				stem + ".test.js", stem + ".spec.js",
			}
		},
		strategy: StrategyCoLocatedTS,
	}
}

// NewVitestJS is the JavaScript-language sibling of NewVitest: the same vitest
// co-located selection, but matching .js/.jsx changed files (langmap resolves
// those to javascript, a distinct token from typescript).
func NewVitestJS() TestSelector {
	return &coLocatedSelector{
		language: langmap.JavaScript,
		langOf:   langmap.LanguageForPath,
		testNames: func(srcBase string) []string {
			stem := stripExt(srcBase)
			return []string{
				stem + ".test.js", stem + ".spec.js",
				stem + ".test.jsx", stem + ".spec.jsx",
				stem + ".test.ts", stem + ".spec.ts",
			}
		},
		strategy: StrategyCoLocatedTS,
	}
}

// stripExt removes the final extension from a base file name (foo.ts → foo).
func stripExt(base string) string {
	for i := len(base) - 1; i >= 0; i-- {
		if base[i] == '.' {
			return base[:i]
		}
	}
	return base
}
