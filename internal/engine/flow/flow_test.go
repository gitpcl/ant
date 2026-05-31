// Package flow_test holds README-level end-to-end integration tests that drive
// the documented `ant` lifecycle — scout -> fix -> review -> apply — through the
// engine entry points each CLI command calls, NOT through package internals
// (Sprint 022 Future-Proofing #5, acceptance: "at least one integration test
// drives scout->fix->review->apply end-to-end and asserts the documented
// behavior").
//
// It is hermetic on purpose: it injects a small scan-safe in-test detector so
// the flow runs without the ast-grep binary (detection is a plugin boundary —
// the species fixtures in species/fixture already prove the real ast-grep path),
// but EVERYTHING downstream of detection is the genuine production pipeline:
//   - scout.Run    publishes findings (read-only, never writes the tree)
//   - colony.Run   runs the REAL deterministic delete-match fixer through the
//     REAL verifier gate and stages a record in a REAL local Store
//   - review       marks the staged record accepted through stage.Area.Mark —
//     the SAME marker the review TUI writes decisions through
//   - apply.Land   preflights and lands the accepted record as a git commit,
//     applying the fixer's real unified-diff to the working tree
//
// The proof is genuine: the fixer's actual output is what apply must apply, so
// an incompatibility between the deterministic fixer's patch format and the
// apply patch primitive would fail HERE, at the documented seam, not only in a
// hand-written-patch unit test.
package flow_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/apply"
	"github.com/gitpcl/ant/internal/engine/colony"
	"github.com/gitpcl/ant/internal/engine/events"
	"github.com/gitpcl/ant/internal/engine/fix"
	"github.com/gitpcl/ant/internal/engine/scout"
	"github.com/gitpcl/ant/internal/engine/stage"
	store "github.com/gitpcl/ant/internal/engine/store"
	"github.com/gitpcl/ant/internal/engine/verify"
)

// the seeded source file: a Go file with one unused import on line 3, the
// canonical mechanically-removable smell the deterministic delete-match fixer
// targets (the unused-import species).
const (
	seededFile    = "main.go"
	seededContent = "package main\n\nimport \"fmt\"\n\nfunc main() {}\n"
	// the verbatim source line the detector "matched" (ast-grep `lines`), which
	// the deterministic fixer turns into the `-` line of its patch. It MUST
	// byte-match the working tree for the patch to apply.
	importLine  = "import \"fmt\""
	importLineN = 3
)

// staticDetector is a scan-safe, ast-grep-free Detector that returns one
// finding for the seeded unused import. Implementing engine.ScanSafeDetector
// (ScanSafe()==true) lets scout admit it on the read-only path, so the flow runs
// the genuine scout.Run composition without depending on the ast-grep binary.
type staticDetector struct{ finding engine.Finding }

func (d staticDetector) Detect(_ context.Context, _ engine.Scope) ([]engine.Finding, error) {
	return []engine.Finding{d.finding}, nil
}
func (staticDetector) ScanSafe() bool { return true }

// compile-time assertion: the in-test detector satisfies the scan-safe interface
// scout enforces, so this test would fail to build if that contract changed.
var _ engine.ScanSafeDetector = staticDetector{}

// seededFinding builds the finding the detector returns and carries the verbatim
// source line so the deterministic fixer's delete-match patch byte-matches the
// working tree (colony.buildFixTask copies SourceLines onto the FixTask context).
func seededFinding() engine.Finding {
	return engine.Finding{
		Species:     "unused-import",
		File:        seededFile,
		Span:        engine.Span{StartLine: importLineN, EndLine: importLineN, StartCol: 1, EndCol: 1},
		Severity:    engine.SeverityMedium,
		Message:     "unused import \"fmt\"",
		Snippet:     importLine,
		SourceLines: importLine,
	}
}

// seededRepo inits a git repo with the seeded file committed, returning its root.
func seededRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	repo, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, seededFile), []byte(seededContent), 0o644); err != nil {
		t.Fatalf("write seeded file: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if _, err := wt.Add(seededFile); err != nil {
		t.Fatalf("Add: %v", err)
	}
	_, err = wt.Commit("initial", &git.CommitOptions{Author: &object.Signature{
		Name: "Test", Email: "t@e", When: time.Unix(0, 0).UTC()}})
	if err != nil {
		t.Fatalf("initial Commit: %v", err)
	}
	return root
}

// drainBus subscribes, runs fn, and returns every event published, so a stage's
// documented event stream (run.start … run.end) can be asserted.
func drainBus(t *testing.T, fn func(bus *events.Bus)) []events.Event {
	t.Helper()
	bus := events.NewBus()
	sub := bus.Subscribe()
	var evs []events.Event
	done := make(chan struct{})
	go func() {
		for ev := range sub.C {
			evs = append(evs, ev)
		}
		close(done)
	}()
	fn(bus)
	bus.Close()
	<-done
	return evs
}

func countType(evs []events.Event, typ events.Type) int {
	n := 0
	for _, ev := range evs {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

// TestScoutFixReviewApplyEndToEnd drives the full documented lifecycle over one
// real git repo and asserts each stage's documented contract.
func TestScoutFixReviewApplyEndToEnd(t *testing.T) {
	root := seededRepo(t)
	finding := seededFinding()
	scope := engine.Scope{Root: root}
	detectors := []engine.NamedDetector{{Species: finding.Species, Detector: staticDetector{finding: finding}}}

	// --- stage 1: scout (read-only) -------------------------------------------
	// Snapshot the file so we can prove scout never writes the working tree.
	before, err := os.ReadFile(filepath.Join(root, seededFile))
	if err != nil {
		t.Fatalf("read seeded file: %v", err)
	}

	var scoutRes scout.Result
	scoutEvs := drainBus(t, func(bus *events.Bus) {
		scoutRes, err = scout.Run(context.Background(), bus, scout.Options{
			Scope:     scope,
			Detectors: detectors,
			RunID:     "fix-flow-1",
		})
	})
	if err != nil {
		t.Fatalf("scout.Run: %v", err)
	}
	if len(scoutRes.Findings) != 1 {
		t.Fatalf("scout findings = %d, want 1 (the seeded unused import)", len(scoutRes.Findings))
	}
	if got := scoutRes.Findings[0]; got.Species != "unused-import" || got.File != seededFile {
		t.Errorf("scout finding = %+v, want unused-import in %s", got, seededFile)
	}
	if countType(scoutEvs, events.TypeRunStart) != 1 || countType(scoutEvs, events.TypeRunEnd) != 1 {
		t.Errorf("scout event stream not well-formed: %d run.start, %d run.end (want 1/1)",
			countType(scoutEvs, events.TypeRunStart), countType(scoutEvs, events.TypeRunEnd))
	}
	if countType(scoutEvs, events.TypeDetectFinding) != 1 {
		t.Errorf("scout detect.finding count = %d, want 1", countType(scoutEvs, events.TypeDetectFinding))
	}
	if after, _ := os.ReadFile(filepath.Join(root, seededFile)); string(after) != string(before) {
		t.Fatalf("scout modified the working tree — it must be read-only")
	}

	// --- stage 2: fix (detect -> REAL fixer -> REAL verify -> stage) ----------
	st := store.New(root)
	if err := st.SaveRun(engine.Run{ID: "fix-flow-1", StartedAt: "2026-05-31T00:00:00Z"}); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	ant := colony.Ant{
		Finding:  finding,
		Fixer:    fix.NewDeterministic(fix.TransformDeleteMatch), // the REAL deterministic fixer
		Verifier: verify.NewGate(verify.DefaultLimits()),         // the REAL gate (diff-bounded)
	}
	var fixRes colony.Result
	drainBus(t, func(bus *events.Bus) {
		fixRes, err = colony.Run(context.Background(), bus, colony.Options{
			Scope:       scope,
			Ants:        []colony.Ant{ant},
			Store:       st,
			RunID:       "fix-flow-1",
			Concurrency: 1,
		})
	})
	if err != nil {
		t.Fatalf("colony.Run: %v", err)
	}
	if fixRes.Verified != 1 || fixRes.Staged != 1 || fixRes.Skipped != 0 {
		t.Fatalf("fix result = %+v, want Verified 1, Staged 1, Skipped 0", fixRes)
	}
	// fix stages but NEVER writes the working tree (the diff is staged, applied later).
	if after, _ := os.ReadFile(filepath.Join(root, seededFile)); string(after) != string(before) {
		t.Fatalf("fix modified the working tree — the diff must be staged, not applied")
	}

	area := stage.New(st, "fix-flow-1")
	records, err := area.ListRecords()
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("staged records = %d, want 1", len(records))
	}
	if records[0].Mark != engine.MarkPending {
		t.Errorf("freshly-staged record Mark = %v, want pending", records[0].Mark)
	}

	// --- stage 3: review (accept the staged record) ---------------------------
	// review.Run is a TUI; its documented OUTCOME is that a reviewer's accept
	// decision is persisted through the stage.Area marker. Drive that marker
	// directly — the exact primitive the TUI's Marker.Mark calls — so the test
	// asserts the documented review->apply hand-off without a terminal.
	if err := area.Mark(0, engine.MarkAccepted); err != nil {
		t.Fatalf("review mark accepted: %v", err)
	}
	reviewed, err := area.ListRecords()
	if err != nil {
		t.Fatalf("ListRecords after review: %v", err)
	}
	if reviewed[0].Mark != engine.MarkAccepted {
		t.Fatalf("record Mark after review = %v, want accepted", reviewed[0].Mark)
	}

	// --- stage 4: apply (preflight + land the accepted record) ----------------
	// `ant apply` lands exactly the accepted set; filter to accepted as the CLI
	// (apply.Drive/acceptedRecords) does, then call the Land entry point.
	accepted := acceptedRecords(reviewed)
	if len(accepted) != 1 {
		t.Fatalf("accepted records = %d, want 1", len(accepted))
	}
	var applyRes apply.Result
	applyDone := 0
	drainEvs := drainBus(t, func(bus *events.Bus) {
		applyRes, err = apply.Land(context.Background(), bus, "fix-flow-1", accepted, apply.Options{
			Root: root,
			Now:  func() time.Time { return time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC) },
		})
	})
	if err != nil {
		t.Fatalf("apply.Land: %v", err)
	}
	applyDone = countType(drainEvs, events.TypeApplyDone)

	// documented apply behavior: a new branch, one commit, one apply.done event.
	if applyRes.Branch != "ant/fix-flow-1" {
		t.Errorf("apply branch = %q, want ant/fix-flow-1", applyRes.Branch)
	}
	if len(applyRes.Commits) != 1 {
		t.Fatalf("apply commits = %d, want 1", len(applyRes.Commits))
	}
	if applyDone != 1 {
		t.Errorf("apply.done events = %d, want 1", applyDone)
	}

	// The fixer's REAL patch landed: the unused import is gone from the working
	// tree on the new branch (proving the whole scout->fix->review->apply chain
	// produced and applied a real, mutually-compatible diff).
	got, err := os.ReadFile(filepath.Join(root, seededFile))
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	want := "package main\n\n\nfunc main() {}\n"
	if string(got) != want {
		t.Errorf("working tree after apply:\n got %q\nwant %q", got, want)
	}

	// HEAD is the new branch with the colony's provenance commit.
	repo, _ := git.PlainOpen(root)
	head, _ := repo.Head()
	if head.Name() != plumbing.NewBranchReferenceName("ant/fix-flow-1") {
		t.Errorf("HEAD = %s, want the new branch", head.Name())
	}
	commit, _ := repo.CommitObject(head.Hash())
	if commit.Author.Name != "Ant Colony" {
		t.Errorf("landed commit author = %q, want Ant Colony", commit.Author.Name)
	}
}

// acceptedRecords mirrors apply.acceptedRecords (unexported): the accepted-set
// filter `ant apply` applies before landing. Reproduced here so the test drives
// the same accepted->Land contract the CLI does.
func acceptedRecords(records []engine.StagedRecord) []engine.StagedRecord {
	out := make([]engine.StagedRecord, 0, len(records))
	for _, r := range records {
		if r.Mark == engine.MarkAccepted {
			out = append(out, r)
		}
	}
	return out
}
