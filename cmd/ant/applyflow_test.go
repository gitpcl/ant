package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/stage"
	store "github.com/gitpcl/ant/internal/engine/store"
)

// TestApplyCLIEndToEnd drives `ant apply` through the CLI against a real go-git
// repo: it stages one ACCEPTED record + one SKIPPED record, runs apply, and
// asserts ONLY the accepted diff lands (on a branch) and the skipped one does
// not. This is the full cmd/ant → engine → go-git path, no `git` binary.
func TestApplyCLIEndToEnd(t *testing.T) {
	root := t.TempDir()

	// A real git repo with one committed file (go-git in-process).
	repo, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nimport \"fmt\"\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	wt, _ := repo.Worktree()
	if _, err := wt.Add("main.go"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := wt.Commit("init", &git.CommitOptions{Author: &object.Signature{Name: "T", Email: "t@e", When: time.Unix(0, 0).UTC()}}); err != nil {
		t.Fatalf("init Commit: %v", err)
	}

	// Save a run and stage an accepted + a skipped record into the Store.
	st := store.New(root)
	if err := st.SaveRun(engine.Run{ID: "fix-run-e2e", StartedAt: "2026-05-24T00:00:00Z"}); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	area := stage.New(st, "fix-run-e2e")
	accepted := engine.StagedRecord{
		Finding: engine.Finding{Species: "unused-import", File: "main.go", Span: engine.Span{StartLine: 3}, Message: "unused import"},
		Diff:    engine.ProposedDiff{Files: []engine.FileDiff{{Path: "main.go", Patch: "--- a/main.go\n+++ b/main.go\n@@ -3,1 +3,0 @@\n-import \"fmt\"\n"}}, Fixer: "deterministic (delete-match)"},
		Verify:  engine.VerifyResult{Passed: true},
		Mark:    engine.MarkAccepted,
	}
	skipped := accepted
	skipped.Finding.File = "other.go"
	skipped.Mark = engine.MarkSkipped
	if err := area.AddRecord(accepted); err != nil {
		t.Fatalf("stage accepted: %v", err)
	}
	if err := area.AddRecord(skipped); err != nil {
		t.Fatalf("stage skipped: %v", err)
	}

	out, code := runCmd(t, "apply", "fix-run-e2e", "--path", root)
	if code != engine.ExitOK {
		t.Fatalf("apply exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, "Applied 1 diff") {
		t.Errorf("apply should report 1 landed diff:\n%s", out)
	}

	// The accepted diff landed: main.go has the import removed, on a new branch.
	got, _ := os.ReadFile(filepath.Join(root, "main.go"))
	if want := "package main\n\n\nfunc main() {}\n"; string(got) != want {
		t.Errorf("accepted diff did not land:\n got %q\nwant %q", got, want)
	}
	head, _ := repo.Head()
	if !strings.Contains(head.Name().String(), "ant/fix-run-e2e") {
		t.Errorf("apply should land on a new branch by default, HEAD = %s", head.Name())
	}
	// The skipped record's file was never created/changed (it does not exist).
	if _, err := os.Stat(filepath.Join(root, "other.go")); !os.IsNotExist(err) {
		t.Errorf("skipped diff must not be applied (other.go should not exist)")
	}
}
