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
