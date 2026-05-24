package apply

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/events"
)

// fixtureRepo inits a git repo in a temp dir with one committed file, returning
// the repo root. go-git inits it in-process — no `git` binary needed.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	repo, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	writeFile(t, root, "main.go", "package main\n\nimport \"fmt\"\n\nfunc main() {}\n")
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if _, err := wt.Add("main.go"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	_, err = wt.Commit("initial", &git.CommitOptions{Author: &object.Signature{
		Name: "Test", Email: "t@e", When: time.Unix(0, 0).UTC()}})
	if err != nil {
		t.Fatalf("initial Commit: %v", err)
	}
	return root
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// deleteImportRecord is a staged record that removes the `import "fmt"` line —
// the kind of diff the deterministic unused-import fixer produces.
func deleteImportRecord(mark engine.Mark) engine.StagedRecord {
	return engine.StagedRecord{
		Finding: engine.Finding{Species: "unused-import", File: "main.go", Span: engine.Span{StartLine: 3}, Message: "unused import \"fmt\""},
		Diff: engine.ProposedDiff{
			Files: []engine.FileDiff{{Path: "main.go", Patch: "--- a/main.go\n+++ b/main.go\n@@ -3,1 +3,0 @@\n-import \"fmt\"\n"}},
			Fixer: "deterministic (delete-match)",
		},
		Verify: engine.VerifyResult{Passed: true},
		Mark:   mark,
	}
}

// fixedClock returns a deterministic commit clock.
func fixedClock() func() time.Time {
	ts := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return ts }
}

func collectApplyDone(t *testing.T, fn func(bus *events.Bus)) []events.ApplyDonePayload {
	t.Helper()
	bus := events.NewBus()
	sub := bus.Subscribe()
	var out []events.ApplyDonePayload
	done := make(chan struct{})
	go func() {
		for ev := range sub.C {
			if ev.Type == events.TypeApplyDone && ev.ApplyDone != nil {
				out = append(out, *ev.ApplyDone)
			}
		}
		close(done)
	}()
	fn(bus)
	bus.Close()
	<-done
	return out
}

// TestLandOnBranchByDefault asserts apply creates a branch, commits the diff on
// it, and emits apply.done with the branch + commit (TECHSPEC §7, colony-view §3.5).
func TestLandOnBranchByDefault(t *testing.T) {
	root := fixtureRepo(t)
	var res Result
	applyDone := collectApplyDone(t, func(bus *events.Bus) {
		var err error
		res, err = Land(context.Background(), bus, "run-1",
			[]engine.StagedRecord{deleteImportRecord(engine.MarkAccepted)},
			Options{Root: root, Now: fixedClock()})
		if err != nil {
			t.Fatalf("Land: %v", err)
		}
	})

	if res.Branch != "ant/fix-run-1" {
		t.Errorf("branch = %q, want ant/fix-run-1", res.Branch)
	}
	if len(res.Commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(res.Commits))
	}

	// The working tree on the new branch has the import removed.
	got, _ := os.ReadFile(filepath.Join(root, "main.go"))
	if want := "package main\n\n\nfunc main() {}\n"; string(got) != want {
		t.Errorf("file not patched on branch:\n got %q\nwant %q", got, want)
	}

	// HEAD is on the new branch with the provenance commit message.
	repo, _ := git.PlainOpen(root)
	head, _ := repo.Head()
	if head.Name() != plumbing.NewBranchReferenceName("ant/fix-run-1") {
		t.Errorf("HEAD = %s, want the new branch", head.Name())
	}
	commit, _ := repo.CommitObject(head.Hash())
	if commit.Message == "" || commit.Author.Name != "Ant Colony" {
		t.Errorf("commit provenance wrong: author=%q msg=%q", commit.Author.Name, commit.Message)
	}

	// apply.done carries the branch + commit.
	if len(applyDone) != 1 || applyDone[0].Branch != "ant/fix-run-1" || applyDone[0].Path != "main.go" {
		t.Errorf("apply.done payload wrong: %+v", applyDone)
	}
}

// TestLandNoBranchOnCurrentBranch asserts --no-branch commits onto the current
// branch (no new branch created) and apply.done has an empty Branch.
func TestLandNoBranchOnCurrentBranch(t *testing.T) {
	root := fixtureRepo(t)
	repo, _ := git.PlainOpen(root)
	headBefore, _ := repo.Head()
	branchBefore := headBefore.Name()

	applyDone := collectApplyDone(t, func(bus *events.Bus) {
		if _, err := Land(context.Background(), bus, "run-2",
			[]engine.StagedRecord{deleteImportRecord(engine.MarkAccepted)},
			Options{Root: root, NoBranch: true, Now: fixedClock()}); err != nil {
			t.Fatalf("Land --no-branch: %v", err)
		}
	})

	headAfter, _ := repo.Head()
	if headAfter.Name() != branchBefore {
		t.Errorf("--no-branch must stay on the current branch: was %s, now %s", branchBefore, headAfter.Name())
	}
	if headAfter.Hash() == headBefore.Hash() {
		t.Error("--no-branch should still create a commit on the current branch")
	}
	if len(applyDone) != 1 || applyDone[0].Branch != "" {
		t.Errorf("apply.done for --no-branch should have empty Branch: %+v", applyDone)
	}
}

// TestLandAppliesOnlyGivenRecords asserts Land lands exactly the records passed
// (the caller filters to accepted/trusted) — an unrelated record is not applied
// because it is simply not in the slice.
func TestLandAppliesOnlyGivenRecords(t *testing.T) {
	root := fixtureRepo(t)
	// Only one accepted record is passed; a skipped one is omitted by the caller.
	applyDone := collectApplyDone(t, func(bus *events.Bus) {
		res, err := Land(context.Background(), bus, "run-3",
			[]engine.StagedRecord{deleteImportRecord(engine.MarkAccepted)},
			Options{Root: root, Now: fixedClock()})
		if err != nil {
			t.Fatalf("Land: %v", err)
		}
		if len(res.Commits) != 1 {
			t.Errorf("expected exactly 1 commit for 1 record, got %d", len(res.Commits))
		}
	})
	if len(applyDone) != 1 {
		t.Errorf("expected exactly 1 apply.done, got %d", len(applyDone))
	}
}

// TestLandNonRepoIsOperational asserts applying in a non-git directory is an
// operational error (exit 2), never a panic — go-git PlainOpen fails cleanly.
func TestLandNonRepoIsOperational(t *testing.T) {
	dir := t.TempDir() // not a git repo
	bus := events.NewBus()
	_, err := Land(context.Background(), bus, "run-x",
		[]engine.StagedRecord{deleteImportRecord(engine.MarkAccepted)}, Options{Root: dir})
	bus.Close()
	if err == nil {
		t.Fatal("applying in a non-repo should error")
	}
	if !isOperational(err) {
		t.Errorf("non-repo apply error should be operational (exit 2): %v", err)
	}
}

// TestLandPathEscapeRefused asserts a diff path escaping the working tree is
// refused (never write outside the repo).
func TestLandPathEscapeRefused(t *testing.T) {
	root := fixtureRepo(t)
	rec := deleteImportRecord(engine.MarkAccepted)
	rec.Diff.Files[0].Path = "../escape.go"
	bus := events.NewBus()
	_, err := Land(context.Background(), bus, "run-esc", []engine.StagedRecord{rec}, Options{Root: root, Now: fixedClock()})
	bus.Close()
	if err == nil {
		t.Fatal("a path escaping the working tree must be refused")
	}
}

// isOperational reports whether err wraps engine.ErrOperational (exit 2).
func isOperational(err error) bool {
	return errors.Is(err, engine.ErrOperational)
}
