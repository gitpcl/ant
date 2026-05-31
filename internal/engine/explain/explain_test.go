package explain

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	store "github.com/gitpcl/ant/internal/engine/store"
)

// seedStore writes a run with two findings into a fresh local store rooted at a
// temp dir, exercising the real persistence path explain resolves against.
func seedStore(t *testing.T) (*store.Store, engine.Run) {
	t.Helper()
	st := store.New(t.TempDir())
	run := engine.Run{
		ID:         "fix-12345",
		StartedAt:  "2026-05-31T10:00:00Z",
		FinishedAt: "2026-05-31T10:00:02Z",
		Scope:      engine.Scope{Root: "."},
		Findings: []engine.Finding{
			{
				Species:  "unused-import",
				File:     "main.go",
				Span:     engine.Span{StartLine: 3, StartCol: 1, EndLine: 3, EndCol: 12},
				Severity: engine.SeverityLow,
				Message:  "unused import \"fmt\"",
				Snippet:  "import \"fmt\"",
			},
			{
				Species:  "n+1-query",
				File:     "repo.go",
				Span:     engine.Span{StartLine: 42, StartCol: 2, EndLine: 45, EndCol: 3},
				Severity: engine.SeverityHigh,
				Message:  "query inside loop",
				Snippet:  "db.Query(...)",
			},
		},
	}
	if err := st.SaveRun(run); err != nil {
		t.Fatalf("seed SaveRun: %v", err)
	}
	return st, run
}

func TestResolveRun(t *testing.T) {
	st, run := seedStore(t)

	d, err := Resolve(st, run.ID)
	if err != nil {
		t.Fatalf("Resolve(run) error: %v", err)
	}
	if d.Kind != KindRun {
		t.Errorf("Kind = %q, want %q", d.Kind, KindRun)
	}
	if d.RunID != run.ID {
		t.Errorf("RunID = %q, want %q", d.RunID, run.ID)
	}
	if d.Run == nil || len(d.Run.Findings) != 2 {
		t.Fatalf("Run not populated with 2 findings: %+v", d.Run)
	}
	if d.Finding != nil {
		t.Errorf("Finding should be nil for a run resolution, got %+v", d.Finding)
	}
}

func TestResolveFinding(t *testing.T) {
	st, run := seedStore(t)

	d, err := Resolve(st, run.ID+"#1")
	if err != nil {
		t.Fatalf("Resolve(finding) error: %v", err)
	}
	if d.Kind != KindFinding {
		t.Errorf("Kind = %q, want %q", d.Kind, KindFinding)
	}
	if d.Index != 1 {
		t.Errorf("Index = %d, want 1", d.Index)
	}
	if d.Finding == nil || d.Finding.Species != "n+1-query" {
		t.Fatalf("Finding not the second finding: %+v", d.Finding)
	}
	if d.Run != nil {
		t.Errorf("Run should be nil for a finding resolution, got %+v", d.Run)
	}
}

func TestResolveMissingRun(t *testing.T) {
	st, _ := seedStore(t)

	_, err := Resolve(st, "does-not-exist")
	if err == nil {
		t.Fatal("Resolve(missing run) should error")
	}
	if !errors.Is(err, engine.ErrRunNotFound) {
		t.Errorf("missing run should be ErrRunNotFound, got %v", err)
	}
	// A missing run is operational (exit 2).
	if engine.ExitCode(err) != engine.ExitOperational {
		t.Errorf("missing run exit = %d, want %d", engine.ExitCode(err), engine.ExitOperational)
	}
}

func TestResolveBadFindingRefs(t *testing.T) {
	st, run := seedStore(t)
	cases := []struct {
		name string
		ref  string
	}{
		{"empty", ""},
		{"index not int", run.ID + "#abc"},
		{"index out of range high", run.ID + "#99"},
		{"index negative", run.ID + "#-1"},
		{"empty run id", "#0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(st, tc.ref)
			if err == nil {
				t.Fatalf("Resolve(%q) should error", tc.ref)
			}
			if !IsBadFindingRef(err) {
				t.Errorf("err for %q should be a bad finding ref, got %v", tc.ref, err)
			}
			if engine.ExitCode(err) != engine.ExitOperational {
				t.Errorf("bad ref exit = %d, want %d", engine.ExitCode(err), engine.ExitOperational)
			}
		})
	}
}

func TestRenderJSONRoundTrip(t *testing.T) {
	st, run := seedStore(t)

	// Run detail JSON round-trips back into a Detail with the run intact.
	d, _ := Resolve(st, run.ID)
	var buf bytes.Buffer
	if err := Render(&buf, FormatJSON, d); err != nil {
		t.Fatalf("Render JSON: %v", err)
	}
	var got Detail
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("JSON does not round-trip: %v\n%s", err, buf.String())
	}
	if got.Kind != KindRun || got.RunID != run.ID {
		t.Errorf("round-tripped detail = %+v, want kind=run id=%s", got, run.ID)
	}
	if got.Run == nil || len(got.Run.Findings) != 2 {
		t.Errorf("round-tripped run findings = %+v, want 2", got.Run)
	}

	// Finding detail JSON carries the located finding.
	df, _ := Resolve(st, run.ID+"#0")
	buf.Reset()
	if err := Render(&buf, FormatJSON, df); err != nil {
		t.Fatalf("Render finding JSON: %v", err)
	}
	if !strings.Contains(buf.String(), "\"kind\": \"finding\"") {
		t.Errorf("finding JSON missing kind=finding:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "unused-import") {
		t.Errorf("finding JSON missing the located finding's species:\n%s", buf.String())
	}
}

func TestRenderHuman(t *testing.T) {
	st, run := seedStore(t)

	d, _ := Resolve(st, run.ID)
	var buf bytes.Buffer
	if err := Render(&buf, FormatHuman, d); err != nil {
		t.Fatalf("Render human: %v", err)
	}
	out := buf.String()
	for _, want := range []string{run.ID, "findings:", "unused-import", "n+1-query"} {
		if !strings.Contains(out, want) {
			t.Errorf("human run output missing %q:\n%s", want, out)
		}
	}

	df, _ := Resolve(st, run.ID+"#1")
	buf.Reset()
	if err := Render(&buf, FormatHuman, df); err != nil {
		t.Fatalf("Render human finding: %v", err)
	}
	out = buf.String()
	for _, want := range []string{"n+1-query", "query inside loop", "repo.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("human finding output missing %q:\n%s", want, out)
		}
	}
}
