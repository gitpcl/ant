package fix_test

import (
	"context"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/fix"
)

// noNetworkContext is a context with no deadline; the deterministic fixer must
// produce its diff purely from the task, so even a context that would fail any
// network dial must not matter. We assert no network another way too (the
// fixer's only inputs are the task fields), but running under a normal context
// documents the "pure transform" expectation.

func TestDeterministicDeleteMatch(t *testing.T) {
	fixer := fix.NewDeterministic(fix.TransformDeleteMatch)

	task := engine.FixTask{
		Finding: engine.Finding{
			Species:  "unused-import",
			File:     "pkg/foo.go",
			Span:     engine.Span{StartLine: 3, StartCol: 1, EndLine: 3, EndCol: 12},
			Severity: engine.SeverityLow,
			Message:  "unused import",
			Snippet:  "import \"os\"",
		},
		Context: engine.CodeContext{
			File:     "pkg/foo.go",
			Language: "go",
			Span:     engine.Span{StartLine: 3, StartCol: 1, EndLine: 3, EndCol: 12},
			Snippet:  "import \"os\"",
		},
	}

	diff, err := fixer.Fix(context.Background(), task)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}

	if len(diff.Files) != 1 {
		t.Fatalf("expected 1 file diff, got %d", len(diff.Files))
	}
	fd := diff.Files[0]
	if fd.Path != "pkg/foo.go" {
		t.Errorf("file path: got %q, want pkg/foo.go", fd.Path)
	}

	wantPatch := "--- a/pkg/foo.go\n" +
		"+++ b/pkg/foo.go\n" +
		"@@ -3,1 +3,0 @@\n" +
		"-import \"os\"\n"
	if fd.Patch != wantPatch {
		t.Errorf("patch mismatch:\n got:\n%q\nwant:\n%q", fd.Patch, wantPatch)
	}

	// Provenance is mandatory and must name the deterministic transform.
	if diff.Fixer != "deterministic (delete-match)" {
		t.Errorf("provenance: got %q, want %q", diff.Fixer, "deterministic (delete-match)")
	}
	if diff.Rationale == "" {
		t.Error("expected a rationale for the explain action")
	}
}

// TestDeterministicMultiLineDelete covers a span that removes more than one line
// (e.g. a dead-code block), asserting the hunk count and the deleted lines.
func TestDeterministicMultiLineDelete(t *testing.T) {
	fixer := fix.NewDeterministic(fix.TransformDeleteMatch)
	snippet := "func dead() {\n\treturn\n}"
	task := engine.FixTask{
		Finding: engine.Finding{
			File:    "dead.go",
			Span:    engine.Span{StartLine: 10, EndLine: 12},
			Snippet: snippet,
		},
		Context: engine.CodeContext{File: "dead.go", Snippet: snippet},
	}

	diff, err := fixer.Fix(context.Background(), task)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	patch := diff.Files[0].Patch
	if !strings.Contains(patch, "@@ -10,3 +10,0 @@\n") {
		t.Errorf("expected a 3-line deletion hunk at line 10, got:\n%s", patch)
	}
	for _, want := range []string{"-func dead() {", "-\treturn", "-}"} {
		if !strings.Contains(patch, want+"\n") {
			t.Errorf("patch missing deleted line %q:\n%s", want, patch)
		}
	}
}

// TestDeterministicDeleteMatchIndented covers the indented-span case: the
// detector captured the verbatim source line WITH its leading tab in
// SourceLines (ast-grep `lines`), and the patch's `-` line must reproduce that
// tab so it byte-matches the working tree (verify/scratch.go exact-matches
// removed lines). Snippet here is the indentation-stripped text the matcher
// emits — the fixer must prefer SourceLines over it.
func TestDeterministicDeleteMatchIndented(t *testing.T) {
	fixer := fix.NewDeterministic(fix.TransformDeleteMatch)

	task := engine.FixTask{
		Finding: engine.Finding{
			Species:     "unused-variable",
			File:        "pkg/foo.go",
			Span:        engine.Span{StartLine: 5, StartCol: 2, EndLine: 5, EndCol: 16},
			Snippet:     "scratch := 1", // stripped of the leading tab
			SourceLines: "\tscratch := 1",
		},
		Context: engine.CodeContext{
			File:        "pkg/foo.go",
			Span:        engine.Span{StartLine: 5, StartCol: 2, EndLine: 5, EndCol: 16},
			Snippet:     "scratch := 1",
			SourceLines: "\tscratch := 1",
		},
	}

	diff, err := fixer.Fix(context.Background(), task)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	wantPatch := "--- a/pkg/foo.go\n" +
		"+++ b/pkg/foo.go\n" +
		"@@ -5,1 +5,0 @@\n" +
		"-\tscratch := 1\n"
	if got := diff.Files[0].Patch; got != wantPatch {
		t.Errorf("indented delete-match patch must preserve the leading tab:\n got:\n%q\nwant:\n%q", got, wantPatch)
	}
}

// TestDeterministicRewrite covers the rewrite transform: a sub-span of a line
// (the `int(x)` conversion) is replaced by the ast-grep `fix:` output (`x`),
// with the rest of the line — indentation and `return ` — preserved. The `-`
// line is the verbatim source line; the `+` line is the same line with the
// matched columns substituted.
func TestDeterministicRewrite(t *testing.T) {
	fixer := fix.NewDeterministic(fix.TransformRewrite)

	// Line: "\treturn int(x)". int(x) spans 0-based cols [8,14); engine Span is
	// 1-based, so StartCol=9, EndCol=15.
	task := engine.FixTask{
		Finding: engine.Finding{
			Species:     "redundant-conversion",
			File:        "conv.go",
			Span:        engine.Span{StartLine: 4, StartCol: 9, EndLine: 4, EndCol: 15},
			Snippet:     "int(x)",
			SourceLines: "\treturn int(x)",
			Replacement: "x",
		},
		Context: engine.CodeContext{
			File:        "conv.go",
			Span:        engine.Span{StartLine: 4, StartCol: 9, EndLine: 4, EndCol: 15},
			Snippet:     "int(x)",
			SourceLines: "\treturn int(x)",
			Replacement: "x",
		},
	}

	diff, err := fixer.Fix(context.Background(), task)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	wantPatch := "--- a/conv.go\n" +
		"+++ b/conv.go\n" +
		"@@ -4,1 +4,1 @@\n" +
		"-\treturn int(x)\n" +
		"+\treturn x\n"
	if got := diff.Files[0].Patch; got != wantPatch {
		t.Errorf("rewrite patch mismatch:\n got:\n%q\nwant:\n%q", got, wantPatch)
	}
	if diff.Fixer != "deterministic (rewrite)" {
		t.Errorf("provenance: got %q, want %q", diff.Fixer, "deterministic (rewrite)")
	}
}

// TestDeterministicRewriteMissingInputs asserts rewrite rejects a missing
// SourceLines or Replacement (a clean per-ant failure the colony turns into a
// skip, not a panic), and rejects a multi-line span.
func TestDeterministicRewriteMissingInputs(t *testing.T) {
	fixer := fix.NewDeterministic(fix.TransformRewrite)
	cases := map[string]engine.FixTask{
		"no source lines": {
			Finding: engine.Finding{File: "a.go", Span: engine.Span{StartLine: 1, StartCol: 1, EndCol: 2}, Replacement: "x"},
			Context: engine.CodeContext{File: "a.go", Replacement: "x"},
		},
		"no replacement": {
			Finding: engine.Finding{File: "a.go", Span: engine.Span{StartLine: 1, StartCol: 1, EndCol: 2}, SourceLines: "ab"},
			Context: engine.CodeContext{File: "a.go", SourceLines: "ab"},
		},
		"multi-line span": {
			Finding: engine.Finding{File: "a.go", Span: engine.Span{StartLine: 1, StartCol: 1, EndCol: 2}, SourceLines: "a\nb", Replacement: "x"},
			Context: engine.CodeContext{File: "a.go", SourceLines: "a\nb", Replacement: "x"},
		},
	}
	for name, task := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := fixer.Fix(context.Background(), task); err == nil {
				t.Errorf("expected an error for %q", name)
			}
		})
	}
}

// TestDeterministicNoNetwork proves the fixer is pure: a fake transport that
// fails any HTTP attempt is installed as the default, and a successful Fix shows
// the deterministic path never dialed it. (No http import in the fixer means no
// way to dial; this guards against a future regression that adds one.)
func TestDeterministicNoNetwork(t *testing.T) {
	dialed := false
	restore := installNoNetworkGuard(t, &dialed)
	defer restore()

	fixer := fix.NewDeterministic(fix.TransformDeleteMatch)
	_, err := fixer.Fix(context.Background(), engine.FixTask{
		Finding: engine.Finding{File: "a.go", Span: engine.Span{StartLine: 1}, Snippet: "x"},
		Context: engine.CodeContext{File: "a.go", Snippet: "x"},
	})
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if dialed {
		t.Error("deterministic fixer made a network call — it must be a pure transform")
	}
}

func TestDeterministicUnknownTransform(t *testing.T) {
	fixer := fix.NewDeterministic("rewrite-everything")
	_, err := fixer.Fix(context.Background(), engine.FixTask{
		Finding: engine.Finding{File: "a.go", Snippet: "x"},
		Context: engine.CodeContext{File: "a.go", Snippet: "x"},
	})
	if err == nil {
		t.Error("expected an error for an unsupported transform")
	}
}

func TestDeterministicMissingInputs(t *testing.T) {
	fixer := fix.NewDeterministic(fix.TransformDeleteMatch)
	t.Run("no path", func(t *testing.T) {
		_, err := fixer.Fix(context.Background(), engine.FixTask{
			Finding: engine.Finding{Snippet: "x"},
			Context: engine.CodeContext{Snippet: "x"},
		})
		if err == nil {
			t.Error("expected an error when no file path is present")
		}
	})
	t.Run("no snippet", func(t *testing.T) {
		_, err := fixer.Fix(context.Background(), engine.FixTask{
			Finding: engine.Finding{File: "a.go"},
			Context: engine.CodeContext{File: "a.go"},
		})
		if err == nil {
			t.Error("expected an error when no snippet is present")
		}
	})
}
