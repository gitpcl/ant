package verify_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/verify"
)

// fakeDetector returns a canned finding set on every Detect, recording the scope
// it was called with so a test can assert it ran over the scratch tree (not the
// original). It models the "re-run the owning detector" step without a live
// ast-grep binary (TECHSPEC §12), mirroring detect.NewRecorded's intent.
type fakeDetector struct {
	result    []engine.Finding
	lastScope engine.Scope
	calls     int
}

func (d *fakeDetector) Detect(_ context.Context, scope engine.Scope) ([]engine.Finding, error) {
	d.calls++
	d.lastScope = scope
	return d.result, nil
}

func targetFinding() engine.Finding {
	return engine.Finding{
		Species:  "unused-import",
		File:     "main.go",
		Span:     engine.Span{StartLine: 3},
		Severity: engine.SeverityLow,
		Message:  "unused import",
	}
}

// minimalTree writes a one-file tree so the scratch copy + patch apply has a
// real file to operate on.
func minimalTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nimport \"os\"\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	return root
}

// patchClearingImport deletes line 3 (the unused import) — a realistic
// delete-match fix.
func patchClearingImport() string {
	return "--- a/main.go\n+++ b/main.go\n@@ -3,1 +3,0 @@\n-import \"os\"\n"
}

// TestDetectorClearsPassesWhenFindingGone: the post-fix re-detect reports NO
// matching finding → pass.
func TestDetectorClearsPassesWhenFindingGone(t *testing.T) {
	root := minimalTree(t)
	det := &fakeDetector{result: nil} // post-fix: detector finds nothing
	v := verify.NewDetectorClears(det, targetFinding())

	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "main.go", Patch: patchClearingImport()}},
		Fixer: "test",
	}
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if !res.Passed {
		t.Fatalf("finding cleared → should pass; got %+v", res)
	}
	if res.Checks[0].Name != verify.CheckDetectorClears {
		t.Errorf("check name = %q, want %q", res.Checks[0].Name, verify.CheckDetectorClears)
	}
	if det.calls != 1 {
		t.Errorf("detector should be re-run exactly once; ran %d times", det.calls)
	}
	// It must have run over the SCRATCH tree, not the original root.
	if det.lastScope.Root == root {
		t.Errorf("detector re-ran over the ORIGINAL root %q; it must run over the scratch copy", root)
	}
}

// TestDetectorClearsFailsWhenFindingPersists: the post-fix re-detect still
// reports the same species+file finding → fail with a detail.
func TestDetectorClearsFailsWhenFindingPersists(t *testing.T) {
	root := minimalTree(t)
	det := &fakeDetector{result: []engine.Finding{targetFinding()}} // still there
	v := verify.NewDetectorClears(det, targetFinding())

	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "main.go", Patch: patchClearingImport()}},
		Fixer: "test",
	}
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if res.Passed {
		t.Fatal("finding persists → must fail")
	}
	if res.Checks[0].Detail == "" {
		t.Error("a persisting finding must carry a detail (the skip reason)")
	}
}

// TestDetectorClearsDifferentFileDoesNotFalsePass: a finding in a DIFFERENT file
// post-fix does not count as the targeted finding persisting; but a remaining
// finding in the SAME file does. This guards the species+file match.
func TestDetectorClearsIgnoresOtherFiles(t *testing.T) {
	root := minimalTree(t)
	other := engine.Finding{Species: "unused-import", File: "other.go", Span: engine.Span{StartLine: 1}}
	det := &fakeDetector{result: []engine.Finding{other}} // a finding, but elsewhere
	v := verify.NewDetectorClears(det, targetFinding())

	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "main.go", Patch: patchClearingImport()}},
		Fixer: "test",
	}
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if !res.Passed {
		t.Fatalf("a finding in a DIFFERENT file must not count as this finding persisting; got %+v", res)
	}
}

// TestDetectorClearsDetectorErrorIsSkip: a detector that errors becomes a failed
// check, never a panic.
func TestDetectorClearsDetectorErrorIsSkip(t *testing.T) {
	root := minimalTree(t)
	v := verify.NewDetectorClears(errDetector{}, targetFinding())
	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "main.go", Patch: patchClearingImport()}},
		Fixer: "test",
	}
	res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
	if res.Passed {
		t.Fatal("a detector error must fail the check")
	}
}

type errDetector struct{}

func (errDetector) Detect(context.Context, engine.Scope) ([]engine.Finding, error) {
	return nil, context.DeadlineExceeded
}

// TestDetectorClearsNeverMutatesRealTree: re-running detection over a scratch
// copy must not touch the original tree.
func TestDetectorClearsNeverMutatesRealTree(t *testing.T) {
	root := minimalTree(t)
	before := hashTree(t, root)

	det := &fakeDetector{result: nil}
	v := verify.NewDetectorClears(det, targetFinding())
	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: "main.go", Patch: patchClearingImport()}},
		Fixer: "test",
	}
	_ = v.Verify(context.Background(), diff, engine.Scope{Root: root})

	if after := hashTree(t, root); before != after {
		t.Fatal("detector-clears MUTATED the real working tree")
	}
}
