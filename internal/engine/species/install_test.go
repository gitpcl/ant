package species

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// validManifest is a minimal well-formed species.toml: an ast-grep detector
// referencing a rule file, a deterministic fix, and one verify check. It loads
// through the execution-free loader, so a folder containing it + the rule file
// is an installable species.
const validManifest = `name = "demo"
description = "a demo species"
severity = "medium"
languages = ["go"]
auto_apply = true

[detector]
kind = "ast-grep"
rule = "detect.yml"

[fix]
kind = "deterministic"
transform = "delete-match"

[verify]
checks = ["compile"]
`

// initRepoFromTree creates a git repo in a fresh temp dir, writes the given
// files (relative path → contents), commits them, and returns the repo root.
// go-git inits + commits in-process — no `git` binary, no network. This is the
// fixture-repo seam the install tests clone from via a local-path CloneFn.
func initRepoFromTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	repo, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		if _, err := wt.Add(filepath.ToSlash(rel)); err != nil {
			t.Fatalf("add %s: %v", rel, err)
		}
	}
	if _, err := wt.Commit("fixture", &git.CommitOptions{Author: &object.Signature{
		Name: "Test", Email: "t@e", When: time.Unix(0, 0).UTC()}}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return root
}

// localCloneFn returns a CloneFn that "clones" srcRepo by doing a real go-git
// PlainClone from the local fixture path into the install temp dir. This
// exercises the real clone code path (go-git, no network) while keeping the test
// hermetic. Critically it uses the SAME mechanism production does, so if go-git
// clone executed repo code, this test would catch it.
func localCloneFn(srcRepo string) func(ctx context.Context, dir, url string) error {
	return func(ctx context.Context, dir, _ string) error {
		_, err := git.PlainCloneContext(ctx, dir, false, &git.CloneOptions{URL: srcRepo, Depth: 1})
		return err
	}
}

// TestInstallRunsNoRepoCode is the APPROACH-GATE spike (recorded in
// progress_log.md). A malicious species repo ships a verify.sh, a setup.sh, and a
// Go file with a //go:generate directive — each of which, IF EXECUTED, would
// write a SENTINEL file. Install must place the (structurally valid) species
// WITHOUT running any of them: the sentinel must never appear, anywhere.
//
// This is the single test that proves the no-exec security property. If it fails
// (sentinel exists), the clone/copy path is executing repo code and the whole
// approach is wrong.
func TestInstallRunsNoRepoCode(t *testing.T) {
	work := t.TempDir()
	sentinel := filepath.Join(work, "PWNED")

	// A would-be-executed script writes the sentinel. We point every script at an
	// absolute path so that if ANY of them ran (from any cwd), we'd see it.
	evilBody := "#!/bin/sh\ntouch " + sentinel + "\n"
	goGen := "package demo\n\n//go:generate sh -c \"touch " + sentinel + "\"\n"

	repo := initRepoFromTree(t, map[string]string{
		"demo/species.toml":  validManifest,
		"demo/detect.yml":    "id: demo\nrule: { pattern: foo }\n",
		"demo/verify.sh":     evilBody,
		"demo/setup.sh":      evilBody,
		"demo/gen.go":        goGen,
		"demo/Makefile":      "all:\n\ttouch " + sentinel + "\n",
		".git-hooks-decoy":   evilBody,
		"post-checkout.hook": evilBody,
	})

	dest := filepath.Join(work, ".ant", "species")
	installed, err := Install(context.Background(), InstallOptions{
		URL:      "file://" + repo,
		DestRoot: dest,
		CloneFn:  localCloneFn(repo),
	})
	if err != nil {
		t.Fatalf("Install: unexpected error: %v", err)
	}

	// The species is structurally valid, so it installs.
	if len(installed) != 1 || installed[0].Name != "demo" {
		t.Fatalf("expected 1 installed species 'demo', got %+v", installed)
	}

	// THE ASSERTION: no script ran, so the sentinel does not exist.
	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Fatalf("SECURITY FAILURE: sentinel %q exists — install executed repo code", sentinel)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unexpected stat error on sentinel: %v", statErr)
	}

	// And the installed folder loads through the execution-free loader.
	m, loadErr := Load(os.DirFS(dest), "demo", "test", nil)
	if loadErr != nil {
		t.Fatalf("installed species does not load: %v", loadErr)
	}
	if m.Name != "demo" {
		t.Fatalf("loaded manifest name = %q, want demo", m.Name)
	}
}

// TestInstallValidSpecies confirms a well-formed repo installs: the folder
// appears under .ant/species/<name>/ with its declared files, and only the
// species folder is copied (repo content outside it — README, .git — is not).
func TestInstallValidSpecies(t *testing.T) {
	work := t.TempDir()
	repo := initRepoFromTree(t, map[string]string{
		"README.md":         "# my species pack\n",
		"demo/species.toml": validManifest,
		"demo/detect.yml":   "id: demo\nrule: { pattern: foo }\n",
	})
	dest := filepath.Join(work, ".ant", "species")

	installed, err := Install(context.Background(), InstallOptions{
		URL: "u", DestRoot: dest, CloneFn: localCloneFn(repo),
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(installed) != 1 || installed[0].Name != "demo" {
		t.Fatalf("got %+v, want one species 'demo'", installed)
	}

	// Declared files present.
	for _, f := range []string{"species.toml", "detect.yml"} {
		if _, err := os.Stat(filepath.Join(dest, "demo", f)); err != nil {
			t.Errorf("expected installed file demo/%s: %v", f, err)
		}
	}
	// Repo content outside the species folder is NOT copied.
	if _, err := os.Stat(filepath.Join(dest, "README.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("README.md must not be copied into .ant/species (got err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "demo", ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".git must never be copied into a species folder (got err=%v)", err)
	}
}

// TestInstallMultipleSpecies confirms a repo with several well-formed species
// folders installs all of them.
func TestInstallMultipleSpecies(t *testing.T) {
	work := t.TempDir()
	second := validManifest // distinct name below
	repo := initRepoFromTree(t, map[string]string{
		"a/species.toml": validManifest,
		"a/detect.yml":   "id: a\nrule: { pattern: x }\n",
		"b/species.toml": replaceName(second, "demo", "beta"),
		"b/detect.yml":   "id: b\nrule: { pattern: y }\n",
	})
	dest := filepath.Join(work, ".ant", "species")

	installed, err := Install(context.Background(), InstallOptions{
		URL: "u", DestRoot: dest, CloneFn: localCloneFn(repo),
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(installed) != 2 {
		t.Fatalf("got %d installed, want 2: %+v", len(installed), installed)
	}
	// Sorted by name: beta, demo.
	if installed[0].Name != "beta" || installed[1].Name != "demo" {
		t.Fatalf("got names %q,%q, want beta,demo", installed[0].Name, installed[1].Name)
	}
}

// TestInstallRejectsNoSpecies confirms a repo with no species.toml anywhere is
// rejected with the typed ErrNoSpecies (operational, exit 2), and nothing is
// written to the destination.
func TestInstallRejectsNoSpecies(t *testing.T) {
	work := t.TempDir()
	repo := initRepoFromTree(t, map[string]string{
		"README.md":   "not a species repo\n",
		"src/main.go": "package main\n",
	})
	dest := filepath.Join(work, ".ant", "species")

	_, err := Install(context.Background(), InstallOptions{
		URL: "u", DestRoot: dest, CloneFn: localCloneFn(repo),
	})
	if !IsNoSpecies(err) {
		t.Fatalf("expected ErrNoSpecies, got %v", err)
	}
	if _, statErr := os.Stat(dest); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("nothing should be written when no species is found (dest exists: %v)", statErr)
	}
}

// TestInstallRejectsMalformedManifest confirms a present-but-malformed
// species.toml is a hard rejection (not silently skipped) and nothing installs.
func TestInstallRejectsMalformedManifest(t *testing.T) {
	work := t.TempDir()
	repo := initRepoFromTree(t, map[string]string{
		// Missing required name/severity/detector — loader rejects.
		"broken/species.toml": "description = \"oops\"\n",
	})
	dest := filepath.Join(work, ".ant", "species")

	_, err := Install(context.Background(), InstallOptions{
		URL: "u", DestRoot: dest, CloneFn: localCloneFn(repo),
	})
	if err == nil {
		t.Fatal("expected malformed-manifest rejection, got nil")
	}
	if !IsMalformed(err) {
		t.Fatalf("expected ErrInvalidManifest, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "broken")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a malformed species must not be placed on disk")
	}
}

// TestInstallRejectsEscapingReference confirms a species.toml referencing a file
// that escapes its own folder (e.g. rule = "../secret") is rejected by the
// loader's mustExist (which fs.Stats a path that resolves outside the folder).
// This is the accumulated "user-species path containment" item: a manifest
// cannot point install at content outside the species folder.
func TestInstallRejectsEscapingReference(t *testing.T) {
	work := t.TempDir()
	escaping := `name = "evil"
severity = "high"
[detector]
kind = "ast-grep"
rule = "../../../../etc/passwd"
[fix]
kind = "deterministic"
[verify]
checks = ["compile"]
`
	repo := initRepoFromTree(t, map[string]string{
		"evil/species.toml": escaping,
	})
	dest := filepath.Join(work, ".ant", "species")

	_, err := Install(context.Background(), InstallOptions{
		URL: "u", DestRoot: dest, CloneFn: localCloneFn(repo),
	})
	if err == nil {
		t.Fatal("expected rejection of a manifest referencing a file outside its folder")
	}
	if !IsMalformed(err) {
		t.Fatalf("expected ErrInvalidManifest for escaping reference, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "evil")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a species with an escaping reference must not be placed on disk")
	}
}

// TestInstallRefusesSymlink confirms the copy boundary refuses a symlink inside a
// species folder rather than recreating/following it — a symlink is a
// write-redirection vector. The species is otherwise structurally valid, so the
// rejection comes from the copy boundary, not the loader.
func TestInstallRefusesSymlink(t *testing.T) {
	work := t.TempDir()
	repo := initRepoFromTree(t, map[string]string{
		"demo/species.toml": validManifest,
		"demo/detect.yml":   "id: demo\nrule: { pattern: foo }\n",
	})
	// Add a symlink inside the species folder pointing outside it, and commit it.
	link := filepath.Join(repo, "demo", "evil-link")
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}
	commitExtra(t, repo, "demo/evil-link")

	dest := filepath.Join(work, ".ant", "species")
	_, err := Install(context.Background(), InstallOptions{
		URL: "u", DestRoot: dest, CloneFn: localCloneFn(repo),
	})
	if err == nil {
		t.Fatal("expected the copy boundary to refuse a symlink in a species folder")
	}
	// The error path leaves no half-installed folder with the link followed.
	if _, statErr := os.Lstat(filepath.Join(dest, "demo", "evil-link")); statErr == nil {
		t.Errorf("symlink must not be recreated in the install target")
	}
}

// TestInstallDefersTrust confirms install grants NO trust: a freshly-installed
// species (with auto_apply=true in its manifest) resolves to propose-only on
// first use because install never touched the trust store, so the
// freshly-installed override (EffectiveTrust) holds it. This is the
// install→run→review chain's first link.
func TestInstallDefersTrust(t *testing.T) {
	work := t.TempDir()
	repo := initRepoFromTree(t, map[string]string{
		"demo/species.toml": validManifest, // auto_apply = true
		"demo/detect.yml":   "id: demo\nrule: { pattern: foo }\n",
	})
	dest := filepath.Join(work, ".ant", "species")
	if _, err := Install(context.Background(), InstallOptions{
		URL: "u", DestRoot: dest, CloneFn: localCloneFn(repo),
	}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Simulate resolution: an installed (OriginUser) species whose configured
	// auto_apply is true. With NO trust state recorded (install wrote none), the
	// freshly-installed override must force propose-only.
	r := Resolved{
		Manifest:           Manifest{Name: "demo"},
		Origin:             OriginUser,
		EffectiveAutoApply: true,
	}
	if EffectiveTrust(r, TrustState{}) {
		t.Fatal("freshly-installed species must be propose-only on first use, not auto-apply")
	}
	// After one review pass, the configured trust applies — the override lifts.
	if !EffectiveTrust(r, TrustState{Reviewed: true}) {
		t.Fatal("after review, the installed species' configured auto-apply should take effect")
	}
}

// replaceName swaps the manifest name for the multi-species fixture.
func replaceName(manifest, from, to string) string {
	return strings.Replace(manifest, "name = \""+from+"\"", "name = \""+to+"\"", 1)
}

// commitExtra stages and commits an additional path into an existing fixture
// repo (used to add a symlink the initial commit did not include).
func commitExtra(t *testing.T, repo, rel string) {
	t.Helper()
	r, err := git.PlainOpen(repo)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if _, err := wt.Add(rel); err != nil {
		t.Fatalf("add %s: %v", rel, err)
	}
	if _, err := wt.Commit("add "+rel, &git.CommitOptions{Author: &object.Signature{
		Name: "Test", Email: "t@e", When: time.Unix(0, 0).UTC()}}); err != nil {
		t.Fatalf("commit %s: %v", rel, err)
	}
}
