package testselect

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gitpcl/ant/internal/engine"
)

// ListCommand returns the raw `go list -json ./...` output for the module rooted
// at dir. It is injectable so the import-graph selector is testable against a
// recorded listing WITHOUT shelling out — and so a real run uses the live
// toolchain on the scratch tree (TECHSPEC §12: CI needs no live build of a
// fixture it did not author). Production uses goListCommand.
type ListCommand func(ctx context.Context, dir string) ([]byte, error)

// importGraphSelector selects every test package that transitively imports a
// changed file's package (TECHSPEC §5.3.1, fallback when no coverage data). It is
// strictly narrower than the full suite: a test package whose closure does not
// reach any changed package is EXCLUDED. It works at package granularity (a Go
// test belongs to a package), so it ignores per-line info — that precision is the
// coverage-map selector's job.
type importGraphSelector struct {
	list ListCommand
}

// compile-time assertion that importGraphSelector satisfies TestSelector.
var _ TestSelector = (*importGraphSelector)(nil)

// NewImportGraph returns the import-graph selector. A nil list falls back to the
// real `go list -json ./...`; tests inject a recorded listing to stay hermetic.
func NewImportGraph(list ListCommand) TestSelector {
	if list == nil {
		list = goListCommand
	}
	return &importGraphSelector{list: list}
}

// goPackage is the subset of `go list -json` fields the selector needs: where the
// package lives (Dir, *Files to map a changed file to its package) and what it
// imports (regular + test, for the transitive closure).
type goPackage struct {
	ImportPath   string   `json:"ImportPath"`
	Dir          string   `json:"Dir"`
	GoFiles      []string `json:"GoFiles"`
	TestGoFiles  []string `json:"TestGoFiles"`  // in-package _test.go
	XTestGoFiles []string `json:"XTestGoFiles"` // external _test.go (package foo_test)
	Imports      []string `json:"Imports"`
	TestImports  []string `json:"TestImports"`
	XTestImports []string `json:"XTestImports"`
}

// Select builds the package graph for the scope module, maps each changed file to
// its owning package, then selects every package that HAS test files and whose
// transitive import closure (regular + test imports) reaches a changed package.
// Returns OK=false when there are no changes or the listing yields no packages
// (so the verifier falls through), never an empty "run nothing" selection.
func (s *importGraphSelector) Select(ctx context.Context, changes []Change, scope engine.Scope) (Selection, error) {
	if len(changes) == 0 {
		return Selection{Strategy: StrategyImportGraph}, nil
	}
	root := scope.Root
	if root == "" {
		root = "."
	}

	raw, err := s.list(ctx, root)
	if err != nil {
		return Selection{}, fmt.Errorf("import-graph: go list failed: %w", err)
	}
	pkgs, err := parseGoList(raw)
	if err != nil {
		return Selection{}, fmt.Errorf("import-graph: parse go list: %w", err)
	}
	if len(pkgs) == 0 {
		return Selection{Strategy: StrategyImportGraph}, nil
	}

	byPath := make(map[string]goPackage, len(pkgs))
	for _, p := range pkgs {
		byPath[p.ImportPath] = p
	}

	// Map each changed file to its owning package import path.
	changedPkgs := changedPackages(changes, root, pkgs)
	if len(changedPkgs) == 0 {
		// A change that touches no Go package (e.g. a README) selects nothing via
		// the graph — fall through so package-fallback can scope to the dir.
		return Selection{Strategy: StrategyImportGraph}, nil
	}

	// Select every test-bearing package whose closure reaches a changed package.
	var selected []string
	seenSel := make(map[string]bool)
	for _, p := range pkgs {
		if len(p.TestGoFiles) == 0 && len(p.XTestGoFiles) == 0 {
			continue // no tests here — nothing to run
		}
		if closureReaches(p, changedPkgs, byPath) {
			if !seenSel[p.ImportPath] {
				seenSel[p.ImportPath] = true
				selected = append(selected, p.ImportPath)
			}
		}
	}
	if len(selected) == 0 {
		// No test package imports the change. Fall through to package-fallback so
		// the changed package's own dir is still scoped (never silently skip).
		return Selection{Strategy: StrategyImportGraph}, nil
	}

	return Selection{
		Tests:    selected,
		Packages: selected,
		Strategy: StrategyImportGraph,
		OK:       true,
	}, nil
}

// changedPackages resolves each changed file to the import path of the package
// whose Dir contains it. A file under a package's Dir belongs to that package.
func changedPackages(changes []Change, root string, pkgs []goPackage) map[string]bool {
	out := make(map[string]bool)
	for _, ch := range changes {
		abs := ch.File
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, ch.File)
		}
		abs = filepath.Clean(abs)
		dir := filepath.Dir(abs)
		for _, p := range pkgs {
			if filepath.Clean(p.Dir) == dir {
				out[p.ImportPath] = true
				break
			}
		}
	}
	return out
}

// closureReaches reports whether package p — counting its own path plus every
// package it imports transitively via regular AND test imports — reaches any of
// the changed packages. Test imports are included so a test that imports a
// changed package (even if the non-test code does not) is still selected.
func closureReaches(p goPackage, changed map[string]bool, byPath map[string]goPackage) bool {
	if changed[p.ImportPath] {
		return true // a package's own tests cover its own change
	}
	visited := make(map[string]bool)
	stack := make([]string, 0, len(p.Imports)+len(p.TestImports)+len(p.XTestImports))
	stack = append(stack, p.Imports...)
	stack = append(stack, p.TestImports...)
	stack = append(stack, p.XTestImports...)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		if changed[cur] {
			return true
		}
		dep, ok := byPath[cur]
		if !ok {
			continue // stdlib / external package outside the module graph
		}
		stack = append(stack, dep.Imports...)
	}
	return false
}

// parseGoList parses the concatenated JSON objects `go list -json` emits (one
// object per package, NOT a JSON array) using a streaming decoder.
func parseGoList(raw []byte) ([]goPackage, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	var pkgs []goPackage
	for dec.More() {
		var p goPackage
		if err := dec.Decode(&p); err != nil {
			return nil, err
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

// goListCommand is the production ListCommand: `go list -json ./...` in dir,
// returning the raw stream for parseGoList. Errors carry stderr so a broken
// module surfaces a real reason, not a bare exit code.
func goListCommand(ctx context.Context, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-json", "./...")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s: %w", strings.TrimSpace(string(ee.Stderr)), err)
		}
		return nil, err
	}
	return out, nil
}
