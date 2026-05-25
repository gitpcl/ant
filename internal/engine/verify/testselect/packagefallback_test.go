package testselect

import (
	"context"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
)

// TestPackageFallbackScopesToChangedDir asserts the last-resort selector scopes to
// the changed file's own package directory ("./internal/foo") and NEVER emits the
// whole-suite pattern ("./..."). This is the §5.3.1 hard rule: the last resort is
// package/dir-scoped, never the full suite.
func TestPackageFallbackScopesToChangedDir(t *testing.T) {
	sel := NewPackageFallback()
	changes := []Change{
		{File: "internal/foo/foo.go"},
		{File: "internal/foo/bar.go"}, // same dir → de-duped
		{File: "internal/baz/baz.go"},
	}
	got, err := sel.Select(context.Background(), changes, engine.Scope{Root: "."})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if !got.OK {
		t.Fatalf("package-fallback must always produce a scoped selection for a non-empty change set")
	}
	want := map[string]bool{"./internal/foo": true, "./internal/baz": true}
	if len(got.Packages) != len(want) {
		t.Fatalf("packages = %v, want the 2 changed dirs", got.Packages)
	}
	for _, p := range got.Packages {
		if !want[p] {
			t.Errorf("unexpected package %q", p)
		}
		if p == "./..." || strings.HasSuffix(p, "/...") {
			t.Fatalf("package-fallback emitted a recursive/whole-suite pattern %q — forbidden by §5.3.1", p)
		}
	}
	if got.Strategy != StrategyPackageFallback {
		t.Errorf("Strategy = %q, want package-fallback", got.Strategy)
	}
}

// TestPackageFallbackRootChange asserts a change to a root-level file scopes to
// "." (the root package only), NOT "./..." (the whole tree).
func TestPackageFallbackRootChange(t *testing.T) {
	sel := NewPackageFallback()
	got, _ := sel.Select(context.Background(), []Change{{File: "main.go"}}, engine.Scope{Root: "."})
	if len(got.Packages) != 1 || got.Packages[0] != "." {
		t.Fatalf("root-file change → packages %v, want [\".\"] (not \"./...\")", got.Packages)
	}
}
