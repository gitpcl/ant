package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCmdImportsEngine asserts cmd/ant depends on the engine — the CLI is a
// front door over the library (TECHSPEC §3), so it must reference it.
func TestCmdImportsEngine(t *testing.T) {
	imports := collectImports(t)
	const enginePkg = "github.com/gitpcl/ant/internal/engine"
	found := false
	for imp := range imports {
		if imp == enginePkg || strings.HasPrefix(imp, enginePkg+"/") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("cmd/ant must import %s (it is a thin front door over the engine)", enginePkg)
	}
}

// TestCmdHasNoForbiddenImports asserts cmd/ant pulls in only flag-parsing,
// rendering, and engine packages — never raw orchestration/persistence
// machinery that belongs in the engine (TECHSPEC §3 hard rule). Concurrency
// primitives, networking, and exec are engine concerns; the CLI must not reach
// for them directly.
func TestCmdHasNoForbiddenImports(t *testing.T) {
	forbidden := map[string]string{
		"sync":          "concurrency orchestration belongs in internal/engine",
		"net/http":      "network/LLM calls belong in internal/engine fix adapters",
		"os/exec":       "shelling out to detectors/harnesses belongs in internal/engine",
		"encoding/json": "JSON event rendering must consume the engine's event bus, not hand-roll encoding",
		"go-git":        "git operations belong in internal/engine apply",
		"database/sql":  "persistence belongs in internal/engine store",
	}
	imports := collectImports(t)
	for imp := range imports {
		for bad, why := range forbidden {
			if imp == bad || strings.Contains(imp, bad) {
				t.Errorf("cmd/ant imports %q — forbidden: %s", imp, why)
			}
		}
	}
}

// TestCmdHasNoBusinessLogicConstructs scans cmd/ant source for AST constructs
// that signal business logic rather than parse+render: goroutines, channels,
// and select statements are colony orchestration and must live in the engine.
func TestCmdHasNoBusinessLogicConstructs(t *testing.T) {
	fset := token.NewFileSet()
	for _, path := range goFiles(t) {
		if strings.HasSuffix(path, "_test.go") {
			continue // tests may legitimately use these constructs
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.GoStmt:
				t.Errorf("%s: goroutine launch is engine orchestration, not CLI rendering", pos(fset, node.Pos()))
			case *ast.SendStmt:
				t.Errorf("%s: channel send is engine orchestration, not CLI rendering", pos(fset, node.Pos()))
			case *ast.SelectStmt:
				t.Errorf("%s: select is engine orchestration, not CLI rendering", pos(fset, node.Pos()))
			}
			return true
		})
	}
}

// collectImports returns the set of import paths used across cmd/ant's
// non-test source files.
func collectImports(t *testing.T) map[string]struct{} {
	t.Helper()
	fset := token.NewFileSet()
	imports := make(map[string]struct{})
	for _, path := range goFiles(t) {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range file.Imports {
			imports[strings.Trim(imp.Path.Value, `"`)] = struct{}{}
		}
	}
	return imports
}

// goFiles lists the .go files in the cmd/ant directory.
func goFiles(t *testing.T) []string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	entries, err := os.ReadDir(wd)
	if err != nil {
		t.Fatalf("readdir %s: %v", wd, err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			files = append(files, filepath.Join(wd, e.Name()))
		}
	}
	if len(files) == 0 {
		t.Fatalf("no .go files found in %s", wd)
	}
	return files
}

func pos(fset *token.FileSet, p token.Pos) string {
	return fset.Position(p).String()
}
