package species

// install.go is the SECURITY-critical install path (TECHSPEC §7): it clones a
// community species repository and places well-formed species folders under the
// on-disk .ant/species/ tree WITHOUT EXECUTING ANY CODE FROM THE REPO.
//
// The single load-bearing property of this file is "install runs no repo code".
// A cloned repo may contain setup scripts, a verify.sh, a Makefile, `go:generate`
// directives, git hooks — none of them run at install. Install does exactly two
// things to repo content: it (1) PARSES species.toml via the existing
// execution-free loader (Load → TOML decode + fs.Stat of referenced files; it
// never runs anything), and (2) byte-copies the validated species folder and its
// declared files into place with a hardened, traversal-/symlink-proof copy. All
// trust is deferred to RUN time, gated by the Sprint 011 freshly-installed
// propose-only override: install deliberately does NOT touch the trust store, so
// a freshly-installed species is absent from the trust map and EffectiveTrust
// forces it propose-only on first use until a human reviews its output once.
//
// Cloning is in-process go-git (PlainCloneContext) — consistent with the
// no-`git`-binary story (TECHSPEC §2) and reusing the go-git dependency already
// used by internal/engine/apply. go-git itself does not run repo code on clone
// (it does not execute hooks or checkout filters that shell out), which is the
// reason a library clone is safe where a shell `git clone` + post-clone script
// would not be.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"

	"github.com/gitpcl/ant/internal/engine"
)

// ErrNoSpecies is returned when a cloned repository contains no well-formed
// species folder (no directory with a parseable species.toml). It wraps
// engine.ErrOperational so the CLI maps it to exit code 2 — a repo with nothing
// installable is a user error, not a crash.
var ErrNoSpecies = fmt.Errorf("%w: species install: no well-formed species folder found in repository", engine.ErrOperational)

// InstallOptions parameterizes an install.
type InstallOptions struct {
	// URL is the git remote to clone. It is passed verbatim to go-git's
	// in-process clone; it is never handed to a shell, so it cannot inject shell
	// commands. An empty URL is rejected.
	URL string
	// DestRoot is the on-disk .ant/species directory the validated folders are
	// copied into (typically "<repo>/.ant/species"). Created if absent.
	DestRoot string
	// CloneFn lets tests inject a clone that populates a local directory from a
	// fixture repo instead of hitting the network. Production passes nil and gets
	// the real go-git PlainCloneContext. The function must clone url into dir and
	// must NOT execute any repo code (the whole point of install).
	CloneFn func(ctx context.Context, dir, url string) error
	// Registry is the kind authority the loader validates against; nil falls back
	// to the default registry.
	Registry *Registry
}

// Installed describes one species placed on disk by an install.
type Installed struct {
	Name string // species name (from the manifest) and on-disk folder name
	Path string // absolute path of the installed folder under DestRoot
}

// Install clones opts.URL into a temporary directory, discovers every
// well-formed species folder in the clone (a directory containing a parseable
// species.toml), and copies ONLY those folders and their manifest-declared files
// into opts.DestRoot/<name>. It returns the installed species sorted by name.
//
// SECURITY CONTRACT (TECHSPEC §7):
//   - No repo code runs. Validation is a TOML parse + fs.Stat via the
//     execution-free loader; placement is a byte copy. No script, hook, generate
//     directive, or Makefile is invoked.
//   - The temp clone is removed before returning (success or failure) so an
//     untrusted working copy never lingers on disk.
//   - Copy is path-contained: every destination path is verified to stay within
//     DestRoot/<name>, absolute paths are refused, and symlinks are refused
//     outright (a symlink in the repo cannot redirect a write outside the
//     install target).
//   - Trust is NOT granted. Install never writes the trust store, so the
//     freshly-installed propose-only override (Sprint 011) holds the species
//     propose-only on first use.
func Install(ctx context.Context, opts InstallOptions) ([]Installed, error) {
	if strings.TrimSpace(opts.URL) == "" {
		return nil, fmt.Errorf("%w: species install: a git URL is required", engine.ErrOperational)
	}
	if opts.DestRoot == "" {
		return nil, fmt.Errorf("%w: species install: destination root is required", engine.ErrOperational)
	}
	reg := opts.Registry
	if reg == nil {
		reg = NewRegistry()
	}
	clone := opts.CloneFn
	if clone == nil {
		clone = gitClone
	}

	// Clone into a temp dir we own and always remove. The clone is untrusted
	// content; it must never outlive the install.
	tmp, err := os.MkdirTemp("", "ant-species-install-*")
	if err != nil {
		return nil, fmt.Errorf("%w: species install: create temp dir: %v", engine.ErrOperational, err)
	}
	defer os.RemoveAll(tmp)

	if err := clone(ctx, tmp, opts.URL); err != nil {
		return nil, fmt.Errorf("%w: species install: clone %q: %v", engine.ErrOperational, opts.URL, err)
	}

	// Discover + validate STRUCTURE ONLY. Each candidate goes through the
	// execution-free loader; a malformed species.toml is rejected with a clear
	// error (wrapping ErrInvalidManifest) and aborts the whole install — we do
	// not silently install a partial/garbage set.
	candidates, err := discoverInstallable(tmp, reg)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, ErrNoSpecies
	}

	if err := os.MkdirAll(opts.DestRoot, 0o755); err != nil {
		return nil, fmt.Errorf("%w: species install: create dest %q: %v", engine.ErrOperational, opts.DestRoot, err)
	}

	out := make([]Installed, 0, len(candidates))
	for _, c := range candidates {
		dest := filepath.Join(opts.DestRoot, c.name)
		if err := copySpeciesFolder(c.absDir, dest); err != nil {
			return nil, err
		}
		out = append(out, Installed{Name: c.name, Path: dest})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// gitClone is the production clone: an in-process, shallow (depth=1) go-git
// clone. go-git does not execute repo hooks or filters, so cloning runs no repo
// code — the property a shell `git clone` cannot guarantee. The URL is a library
// argument, never a shell word, so it cannot inject commands.
func gitClone(ctx context.Context, dir, url string) error {
	_, err := git.PlainCloneContext(ctx, dir, false, &git.CloneOptions{
		URL:   url,
		Depth: 1, // shallow: we only need the tip to validate + copy structure
		Tags:  git.NoTags,
	})
	return err
}

// installCandidate is a validated species folder in the clone, ready to copy.
type installCandidate struct {
	name   string // manifest Name == on-disk folder name under DestRoot
	absDir string // absolute path of the folder within the clone
}

// discoverInstallable walks the clone for every directory containing a
// species.toml and validates each through the execution-free loader. A folder
// validates iff Load succeeds (TOML parses, required fields present, referenced
// files exist within the folder). A malformed manifest is a hard error naming
// the offending folder — install refuses to place a broken species.
//
// The .git directory is skipped: it is repository metadata, never a species, and
// must not be copied into .ant/species.
func discoverInstallable(cloneRoot string, reg *Registry) ([]installCandidate, error) {
	var out []installCandidate
	seen := map[string]string{} // species name → first dir that declared it

	walkErr := filepath.WalkDir(cloneRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != ManifestFileName {
			return nil
		}
		// p is "<dir>/species.toml"; the species folder is its parent.
		folder := filepath.Dir(p)
		rel, relErr := filepath.Rel(cloneRoot, folder)
		if relErr != nil {
			return relErr
		}
		// Load reads the manifest through an FS rooted at the clone, using the
		// folder's relative path — exactly how the resolver validates user/built-in
		// trees. Load runs NO repo code (TOML parse + fs.Stat only).
		m, loadErr := Load(os.DirFS(cloneRoot), filepath.ToSlash(rel), "install:"+filepath.ToSlash(rel), reg)
		if loadErr != nil {
			// A present-but-malformed species.toml is a hard rejection: a community
			// repo that ships a broken manifest must fail loudly, not be skipped.
			return loadErr
		}
		if prev, dup := seen[m.Name]; dup {
			return fmt.Errorf("%w: species install: repository declares species %q twice (%s and %s)",
				engine.ErrOperational, m.Name, prev, rel)
		}
		seen[m.Name] = rel
		out = append(out, installCandidate{name: m.Name, absDir: folder})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return out, nil
}

// copySpeciesFolder byte-copies the tree rooted at srcDir into destDir with a
// hardened, traversal-/symlink-proof copy. It is the single write boundary for
// untrusted repo content, so it enforces the containment property directly:
//
//   - Every entry's destination is computed with filepath.Join under destDir and
//     re-verified to stay within destDir (a relative path containing ".." that
//     escapes is refused — never trust a path derived from cloned content).
//   - Symlinks are REFUSED outright (not followed, not recreated). A symlink in
//     the repo is the classic escape: a copy that recreated or followed it could
//     redirect a later write outside .ant/species/<name>. Refusing them removes
//     the vector entirely. (A species folder is plain config + rule/prompt files;
//     it has no legitimate need for symlinks.)
//   - Regular files only; irregular entries (devices, sockets, FIFOs) are refused.
//
// destDir is removed first so a reinstall is a clean replace, not a merge over
// stale files.
func copySpeciesFolder(srcDir, destDir string) error {
	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return fmt.Errorf("%w: species install: resolve dest %q: %v", engine.ErrOperational, destDir, err)
	}
	if err := os.RemoveAll(absDest); err != nil {
		return fmt.Errorf("%w: species install: clear dest %q: %v", engine.ErrOperational, destDir, err)
	}

	return filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(srcDir, p)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return os.MkdirAll(absDest, 0o755)
		}

		// Containment check: the destination must stay strictly within absDest.
		// This guards against a crafted relative path (".." segments) in the
		// source tree redirecting the write outside the install target.
		target, joinErr := containedJoin(absDest, rel)
		if joinErr != nil {
			return joinErr
		}

		// Reject symlinks and other irregular files by inspecting the entry type
		// WITHOUT following it (WalkDir does not follow symlinks; we never call a
		// path-resolving Stat on the link). A symlink cannot be copied and must
		// not be recreated — it is a write-redirection vector.
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		mode := info.Mode()
		if mode&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: species install: refusing symlink %q in species folder (symlinks are not allowed)",
				engine.ErrOperational, rel)
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !mode.IsRegular() {
			return fmt.Errorf("%w: species install: refusing non-regular file %q in species folder",
				engine.ErrOperational, rel)
		}
		return copyFile(p, target, mode.Perm())
	})
}

// containedJoin joins rel under root and verifies the cleaned result stays
// within root, refusing absolute paths and ".." escapes. It mirrors the
// containment discipline already used at the apply write boundary
// (apply.safeJoin) so untrusted paths cannot write outside the install target.
func containedJoin(root, rel string) (string, error) {
	cleaned := filepath.Clean(rel)
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("%w: species install: absolute path %q not allowed", engine.ErrOperational, rel)
	}
	full := filepath.Join(root, cleaned)
	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", fmt.Errorf("%w: species install: resolve %q: %v", engine.ErrOperational, rel, err)
	}
	if absFull != root && !hasPathPrefix(absFull, root) {
		return "", fmt.Errorf("%w: species install: path %q escapes the install target", engine.ErrOperational, rel)
	}
	return absFull, nil
}

// hasPathPrefix reports whether p is within dir (dir + separator prefix). Same
// logic as the apply boundary's check, duplicated here so the species package
// owns its own containment without importing the apply package.
func hasPathPrefix(p, dir string) bool {
	if dir == "" {
		return true
	}
	withSep := dir
	if withSep[len(withSep)-1] != filepath.Separator {
		withSep += string(filepath.Separator)
	}
	return len(p) >= len(withSep) && p[:len(withSep)] == withSep
}

// copyFile copies a single regular file's bytes from src to dst with the given
// permission bits. It opens src read-only (no execution) and never invokes it.
func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("%w: species install: open %q: %v", engine.ErrOperational, src, err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("%w: species install: create dir for %q: %v", engine.ErrOperational, dst, err)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("%w: species install: create %q: %v", engine.ErrOperational, dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("%w: species install: copy into %q: %v", engine.ErrOperational, dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("%w: species install: close %q: %v", engine.ErrOperational, dst, err)
	}
	return nil
}

// IsNoSpecies reports whether err is the "no installable species" condition, so
// the CLI can render a tailored message. A thin wrapper over errors.Is kept here
// beside the sentinel.
func IsNoSpecies(err error) bool { return errors.Is(err, ErrNoSpecies) }
