package colony

import (
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/langmap"
)

// TestBuildFixTaskPopulatesLanguage asserts the dormant CodeContext.Language is
// now populated from the finding's file via the single langmap authority
// (Sprint 026), for each language a species wave covers, plus the Unknown
// fallback — and that the rest of the FixTask is unchanged (no behavior change
// beyond richer prompt context).
func TestBuildFixTaskPopulatesLanguage(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{"internal/engine/types.go", langmap.Go},
		{"app/Models/User.php", langmap.PHP},
		{"src/util.py", langmap.Python},
		{"components/App.ts", langmap.TypeScript},
		{"components/App.tsx", langmap.TypeScript},
		{"legacy/index.js", langmap.JavaScript},
		{"views/Home.vue", langmap.Vue},
		{"README.md", langmap.Unknown},
	}
	for _, c := range cases {
		f := engine.Finding{
			Species: "demo",
			File:    c.file,
			Snippet: "x",
			Span:    engine.Span{StartLine: 1, EndLine: 1},
		}
		task := buildFixTask(f)
		if task.Context.Language != c.want {
			t.Errorf("buildFixTask(%q).Context.Language = %q, want %q", c.file, task.Context.Language, c.want)
		}
		// The rest of the context still mirrors the finding (unchanged behavior).
		if task.Context.File != c.file {
			t.Errorf("Context.File = %q, want %q", task.Context.File, c.file)
		}
		if task.Finding.Species != "demo" {
			t.Errorf("Finding round-trip broken: %+v", task.Finding)
		}
	}
}
