package local

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
)

func sampleRun(id string) engine.Run {
	return engine.Run{
		ID:        id,
		StartedAt: "2026-05-24T10:00:00Z",
		Scope: engine.Scope{
			Root:    "/repo",
			Species: []string{"unused-import"},
		},
		Findings: []engine.Finding{
			{
				Species:  "unused-import",
				File:     "main.go",
				Span:     engine.Span{StartLine: 3, StartCol: 1, EndLine: 3, EndCol: 12},
				Severity: engine.SeverityHigh,
				Message:  "unused import \"fmt\"",
				Snippet:  "import \"fmt\"",
				Meta:     map[string]string{"rule": "unused-import"},
			},
		},
	}
}

func sampleDiff(fixer string) engine.ProposedDiff {
	return engine.ProposedDiff{
		Files:     []engine.FileDiff{{Path: "main.go", Patch: "@@ -3 +3 @@\n-import \"fmt\"\n"}},
		Fixer:     fixer,
		Rationale: "removes an import with no references",
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	run := sampleRun("run-1")

	if err := s.SaveRun(run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	got, err := s.LoadRun("run-1")
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if !reflect.DeepEqual(got, run) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, run)
	}
}

func TestSaveRunEmptyID(t *testing.T) {
	s := New(t.TempDir())
	if err := s.SaveRun(engine.Run{}); err == nil {
		t.Errorf("SaveRun with empty ID should error")
	}
}

func TestLoadRunMissingTypedError(t *testing.T) {
	s := New(t.TempDir())
	_, err := s.LoadRun("does-not-exist")
	if err == nil {
		t.Fatalf("LoadRun of missing run should error")
	}
	if !errors.Is(err, engine.ErrRunNotFound) {
		t.Errorf("error %v should satisfy errors.Is(engine.ErrRunNotFound)", err)
	}
	var rnf *engine.RunNotFoundError
	if !errors.As(err, &rnf) {
		t.Errorf("error should be *engine.RunNotFoundError, got %T", err)
	} else if rnf.ID != "does-not-exist" {
		t.Errorf("RunNotFoundError.ID = %q, want %q", rnf.ID, "does-not-exist")
	}
}

func TestStageListRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	run := sampleRun("run-2")
	if err := s.SaveRun(run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	d1 := sampleDiff("deterministic (delete-match)")
	d2 := sampleDiff("rawmodel (qwen2.5-coder)")
	if err := s.StageDiff("run-2", d1); err != nil {
		t.Fatalf("StageDiff d1: %v", err)
	}
	if err := s.StageDiff("run-2", d2); err != nil {
		t.Fatalf("StageDiff d2: %v", err)
	}

	staged, err := s.ListStaged("run-2")
	if err != nil {
		t.Fatalf("ListStaged: %v", err)
	}
	want := []engine.ProposedDiff{d1, d2} // order preserved
	if !reflect.DeepEqual(staged, want) {
		t.Errorf("staged mismatch:\n got %+v\nwant %+v", staged, want)
	}
}

func TestStageDiffMissingRun(t *testing.T) {
	s := New(t.TempDir())
	err := s.StageDiff("ghost", sampleDiff("x"))
	if !errors.Is(err, engine.ErrRunNotFound) {
		t.Errorf("StageDiff against missing run: want ErrRunNotFound, got %v", err)
	}
}

func TestListStagedMissingRun(t *testing.T) {
	s := New(t.TempDir())
	_, err := s.ListStaged("ghost")
	if !errors.Is(err, engine.ErrRunNotFound) {
		t.Errorf("ListStaged against missing run: want ErrRunNotFound, got %v", err)
	}
}

func TestListStagedEmptyForKnownRun(t *testing.T) {
	s := New(t.TempDir())
	if err := s.SaveRun(sampleRun("run-3")); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	staged, err := s.ListStaged("run-3")
	if err != nil {
		t.Fatalf("ListStaged: %v", err)
	}
	if len(staged) != 0 {
		t.Errorf("expected no staged diffs, got %d", len(staged))
	}
}

// TestSurvivesProcessRestart simulates a process restart: a completely fresh
// Store instance over the same base directory must read back everything a
// prior Store wrote. This proves persistence is on disk, not in memory.
func TestSurvivesProcessRestart(t *testing.T) {
	dir := t.TempDir()
	run := sampleRun("run-restart")
	diff := sampleDiff("deterministic (delete-match)")

	// First "process".
	writer := New(dir)
	if err := writer.SaveRun(run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	if err := writer.StageDiff("run-restart", diff); err != nil {
		t.Fatalf("StageDiff: %v", err)
	}

	// Confirm state landed under .ant/state on disk.
	if _, err := os.Stat(filepath.Join(dir, stateDir, runsDir, "run-restart.json")); err != nil {
		t.Fatalf("run file not on disk: %v", err)
	}

	// Second "process": a brand-new Store sharing only the base path.
	reader := New(dir)
	gotRun, err := reader.LoadRun("run-restart")
	if err != nil {
		t.Fatalf("LoadRun after restart: %v", err)
	}
	if !reflect.DeepEqual(gotRun, run) {
		t.Errorf("run did not survive restart:\n got %+v\nwant %+v", gotRun, run)
	}
	gotStaged, err := reader.ListStaged("run-restart")
	if err != nil {
		t.Fatalf("ListStaged after restart: %v", err)
	}
	if !reflect.DeepEqual(gotStaged, []engine.ProposedDiff{diff}) {
		t.Errorf("staged did not survive restart:\n got %+v\nwant %+v", gotStaged, []engine.ProposedDiff{diff})
	}
}
