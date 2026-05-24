package apply

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/events"
)

// commitAuthor is the identity recorded on commits Ant lands. It is a fixed
// bot identity (not the user's git config) so provenance is unambiguous: a
// reviewer can see at a glance which commits the colony produced.
var commitAuthor = object.Signature{Name: "Ant Colony", Email: "ant@localhost"}

// Options parameterizes an apply landing.
type Options struct {
	// Root is the working-tree root: an existing git repository (go-git
	// PlainOpen). A non-repo is an operational error (exit 2).
	Root string
	// NoBranch lands the accepted diffs on the CURRENT branch. The default
	// (NoBranch=false) creates and checks out a new branch first, so the change
	// is isolated and reviewable (TECHSPEC §7, review-interaction.md).
	NoBranch bool
	// BranchName is the branch to create when NoBranch is false. Empty defaults
	// to "ant/fix-<runID>".
	BranchName string
	// Now supplies the commit timestamp; nil uses time.Now (a fixed clock makes
	// tests deterministic).
	Now func() time.Time
}

// Result reports what a landing did: the branch landed on (empty for the current
// branch) and the per-diff commit hashes in apply order.
type Result struct {
	Branch  string
	Commits []string
}

// Land applies the given staged records into the working tree as commits and
// emits apply.done per landed diff through the bus (colony-view.md §3.5,
// TECHSPEC §11). It is the engine entry point both `ant apply` (accepted diffs)
// and `ant fix --apply` (trusted diffs) call — the caller has already filtered
// the records to exactly the set that should land, so Land applies all of them.
//
// Branch by default: create+checkout "ant/fix-<runID>" then commit each diff on
// it. --no-branch: commit onto the current branch. Each record becomes one
// commit whose message carries the species + fixer provenance. A patch that does
// not apply cleanly aborts the landing with an operational error BEFORE
// committing that diff, so a bad patch never half-lands.
func Land(ctx context.Context, bus *events.Bus, runID string, records []engine.StagedRecord, opts Options) (Result, error) {
	clock := opts.Now
	if clock == nil {
		clock = time.Now
	}

	repo, err := git.PlainOpen(opts.Root)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %s is not a git repository (apply needs one — TECHSPEC §2): %v",
			engine.ErrOperational, opts.Root, err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return Result{}, fmt.Errorf("%w: open worktree at %s: %v", engine.ErrOperational, opts.Root, err)
	}

	branch := ""
	if !opts.NoBranch {
		branch = opts.BranchName
		if branch == "" {
			branch = "ant/fix-" + runID
		}
		ref := plumbing.NewBranchReferenceName(branch)
		if err := wt.Checkout(&git.CheckoutOptions{Branch: ref, Create: true}); err != nil {
			return Result{}, fmt.Errorf("%w: create+checkout branch %q: %v", engine.ErrOperational, branch, err)
		}
	}

	result := Result{Branch: branch}
	for _, rec := range records {
		commit, err := landRecord(wt, opts.Root, rec, clock())
		if err != nil {
			return result, err // operational; partial landing is reported via result
		}
		result.Commits = append(result.Commits, commit.String())

		// Emit one apply.done per landed FileDiff path so the live view / --json
		// can show each landed file (colony-view.md §3.5). All files in a record
		// share the one commit.
		for _, fd := range rec.Diff.Files {
			bus.Publish(events.Event{Type: events.TypeApplyDone, ApplyDone: &events.ApplyDonePayload{
				RunID: runID, Path: fd.Path, Branch: branch, Commit: commit.String(),
			}})
		}
	}
	return result, nil
}

// landRecord applies every FileDiff in one staged record to the working tree,
// stages the touched files, and commits them as a single commit with provenance
// in the message. It returns the commit hash. A patch that fails to apply aborts
// before any commit so the record never half-lands.
func landRecord(wt *git.Worktree, root string, rec engine.StagedRecord, when time.Time) (plumbing.Hash, error) {
	for _, fd := range rec.Diff.Files {
		if err := applyFileDiffToTree(root, fd); err != nil {
			return plumbing.ZeroHash, err
		}
		if _, err := wt.Add(fd.Path); err != nil {
			return plumbing.ZeroHash, fmt.Errorf("%w: stage %s: %v", engine.ErrOperational, fd.Path, err)
		}
	}

	author := commitAuthor
	author.When = when
	msg := commitMessage(rec)
	hash, err := wt.Commit(msg, &git.CommitOptions{Author: &author})
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("%w: commit fix for %s: %v",
			engine.ErrOperational, rec.Finding.File, err)
	}
	return hash, nil
}

// applyFileDiffToTree reads the target file under root, applies the unified-diff
// patch, and writes the result back. The path is joined under root and cleaned;
// a path escaping the root is refused (never write outside the working tree).
func applyFileDiffToTree(root string, fd engine.FileDiff) error {
	full, err := safeJoin(root, fd.Path)
	if err != nil {
		return err
	}
	src, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("%w: read %s for patch: %v", engine.ErrOperational, fd.Path, err)
	}
	patched, err := applyUnifiedPatch(string(src), fd.Patch)
	if err != nil {
		return fmt.Errorf("%w: apply patch to %s: %v", engine.ErrOperational, fd.Path, err)
	}
	info, statErr := os.Stat(full)
	perm := os.FileMode(0o644)
	if statErr == nil {
		perm = info.Mode().Perm()
	}
	if err := os.WriteFile(full, []byte(patched), perm); err != nil {
		return fmt.Errorf("%w: write patched %s: %v", engine.ErrOperational, fd.Path, err)
	}
	return nil
}

// safeJoin joins rel under root and verifies the result stays within root, so a
// crafted diff path (e.g. "../../etc/passwd") can never write outside the
// working tree — never trust the path on a staged record at the file-write
// boundary.
func safeJoin(root, rel string) (string, error) {
	cleaned := filepath.Clean(rel)
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("%w: diff path %q is absolute (must be repo-relative)", engine.ErrOperational, rel)
	}
	full := filepath.Join(root, cleaned)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("%w: resolve root %s: %v", engine.ErrOperational, root, err)
	}
	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", fmt.Errorf("%w: resolve path %s: %v", engine.ErrOperational, rel, err)
	}
	if absFull != absRoot && !hasPathPrefix(absFull, absRoot) {
		return "", fmt.Errorf("%w: diff path %q escapes the working tree", engine.ErrOperational, rel)
	}
	return full, nil
}

// hasPathPrefix reports whether path is within dir (dir + separator prefix).
func hasPathPrefix(path, dir string) bool {
	if dir == "" {
		return true
	}
	withSep := dir
	if withSep[len(withSep)-1] != filepath.Separator {
		withSep += string(filepath.Separator)
	}
	return len(path) >= len(withSep) && path[:len(withSep)] == withSep
}

// commitMessage builds a one-subject + provenance-body commit message so the
// landed history records WHICH species found the issue and WHICH fixer produced
// the patch (PRD §6.4 provenance everywhere).
func commitMessage(rec engine.StagedRecord) string {
	subject := fmt.Sprintf("fix(%s): %s", rec.Finding.Species, rec.Finding.Message)
	if rec.Finding.Message == "" {
		subject = fmt.Sprintf("fix(%s): %s", rec.Finding.Species, rec.Finding.File)
	}
	return fmt.Sprintf("%s\n\nfixer: %s\nfinding: %s:%d\n",
		subject, rec.Diff.Fixer, rec.Finding.File, rec.Finding.Span.StartLine)
}
