package testselect

import (
	"context"
	"path/filepath"
	"sort"

	"github.com/gitpcl/ant/internal/engine"
)

// packageFallbackSelector is the LAST RESORT (TECHSPEC §5.3.1): when neither
// coverage data nor the import graph could narrow the selection, it runs the tests
// in the changed file's own package/directory. This is coarse — but it is still
// SCOPED to the touched package, NEVER a full-suite `go test ./...`. A full-suite
// fallback is the §5.3.1 failure mode (it collapses confidence: the developer can
// no longer tell precise from coarse), so this selector deliberately stops at the
// changed package's directory.
//
// It always reports StrategyPackageFallback so the coarseness is visible in
// provenance — the whole point is the developer SEES the fix was checked only
// coarsely, and can rerun with coverage if they want more.
type packageFallbackSelector struct{}

// compile-time assertion that packageFallbackSelector satisfies TestSelector.
var _ TestSelector = (*packageFallbackSelector)(nil)

// NewPackageFallback returns the package/dir-scoped last-resort selector.
func NewPackageFallback() TestSelector { return &packageFallbackSelector{} }

// Select maps each changed file to its directory and selects a package pattern
// scoped to that directory (e.g. "./internal/foo"), de-duplicated. It returns
// OK=true whenever there is at least one changed file — it is the last resort and
// must always produce a scoped selection so the verifier never has to fall back to
// the whole suite. With no changes it returns OK=false (nothing to check).
func (s *packageFallbackSelector) Select(_ context.Context, changes []Change, scope engine.Scope) (Selection, error) {
	if len(changes) == 0 {
		return Selection{Strategy: StrategyPackageFallback}, nil
	}
	root := scope.Root
	if root == "" {
		root = "."
	}

	seen := make(map[string]bool)
	var pkgs []string
	for _, ch := range changes {
		dir := filepath.Dir(filepath.ToSlash(ch.File))
		pattern := dirPattern(dir)
		if !seen[pattern] {
			seen[pattern] = true
			pkgs = append(pkgs, pattern)
		}
	}
	if len(pkgs) == 0 {
		return Selection{Strategy: StrategyPackageFallback}, nil
	}
	sort.Strings(pkgs)

	return Selection{
		Tests:    pkgs,
		Packages: pkgs,
		Strategy: StrategyPackageFallback,
		OK:       true,
	}, nil
}

// dirPattern turns a changed file's directory into a single-package `go test`
// pattern relative to the module root. The "." and "" dir become "." (the root
// package only — NOT "./..." which would be the whole tree). A nested dir becomes
// "./<dir>" — exactly that package, no "/..." recursion, so the last resort stays
// scoped to the touched package.
func dirPattern(dir string) string {
	if dir == "" || dir == "." {
		return "."
	}
	return "./" + dir
}
