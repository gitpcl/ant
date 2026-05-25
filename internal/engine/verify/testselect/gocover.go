package testselect

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// goProfileGenerator is the production ProfileGenerator for Go modules. It records
// per-test-package coverage with `go test -coverpkg=./... -coverprofile`, so a
// covered block is attributed to the test package whose run produced it, and
// fingerprints the test-file set by name+modtime so the cache regenerates only
// when tests change (TECHSPEC §5.3.1).
//
// It is constructed against the SCRATCH tree by the verifier (the diff is already
// applied), so coverage reflects the post-fix code without touching the real tree.
type goProfileGenerator struct {
	// modulePath is the module import path (from `go list -m`), trimmed from
	// profile file paths so blocks are module-relative. Resolved lazily.
	modulePath string
}

// NewGoProfileGenerator returns the live Go coverage generator. Tests inject a
// fake ProfileGenerator instead to stay hermetic; this one shells out to the real
// toolchain and is used in production by the verifier.
func NewGoProfileGenerator() ProfileGenerator { return &goProfileGenerator{} }

// Fingerprint hashes the sorted (relative path, size, modtime) of every *_test.go
// file under root. It is deliberately cheap (a filesystem walk, no compilation)
// so the cache can call it on every Get to decide reuse-vs-regenerate. Changing a
// non-test source file leaves the fingerprint stable (cache reused); adding,
// removing, or editing a test file changes it (cache regenerated).
func (g *goProfileGenerator) Fingerprint(_ context.Context, root string) (string, error) {
	h := sha256.New()
	var entries []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		rel, _ := filepath.Rel(root, path)
		entries = append(entries, fmt.Sprintf("%s|%d|%d", filepath.ToSlash(rel), info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("fingerprint walk: %w", err)
	}
	sort.Strings(entries) // stable regardless of walk order
	for _, e := range entries {
		h.Write([]byte(e))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Generate records one coverage profile per test-bearing package by running
// `go test -coverpkg=./... -coverprofile=<tmp> <pkg>` for each, parsing the
// output into CoverBlocks attributed to that test package. A package whose tests
// fail to run is skipped (its absence just means no coverage from it — the
// verifier still has the other strategies); a total failure to list packages is a
// returned error.
func (g *goProfileGenerator) Generate(ctx context.Context, root string) (ProfileSet, error) {
	if g.modulePath == "" {
		mp, err := goModulePath(ctx, root)
		if err != nil {
			return ProfileSet{}, err
		}
		g.modulePath = mp
	}
	modulePrefix := g.modulePath + "/"

	raw, err := goListCommand(ctx, root)
	if err != nil {
		return ProfileSet{}, fmt.Errorf("coverage generate: list packages: %w", err)
	}
	pkgs, err := parseGoList(raw)
	if err != nil {
		return ProfileSet{}, fmt.Errorf("coverage generate: parse list: %w", err)
	}

	tmp, err := os.MkdirTemp("", "ant-cover-")
	if err != nil {
		return ProfileSet{}, fmt.Errorf("coverage generate: temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	var set ProfileSet
	for _, p := range pkgs {
		if len(p.TestGoFiles) == 0 && len(p.XTestGoFiles) == 0 {
			continue
		}
		out := filepath.Join(tmp, sanitize(p.ImportPath)+".out")
		cmd := exec.CommandContext(ctx, "go", "test",
			"-coverpkg=./...", "-coverprofile="+out, "-count=1", p.ImportPath)
		cmd.Dir = root
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		if err := cmd.Run(); err != nil {
			continue // failing/uncoverable package contributes no coverage; skip it
		}
		body, rerr := os.ReadFile(out)
		if rerr != nil {
			continue
		}
		prof, perr := ParseProfile(p.ImportPath, modulePrefix, body)
		if perr != nil {
			return ProfileSet{}, fmt.Errorf("coverage generate: %w", perr)
		}
		set.Profiles = append(set.Profiles, prof)
	}
	return set, nil
}

// goModulePath returns the module import path via `go list -m`, used to make
// profile file paths module-relative.
func goModulePath(ctx context.Context, root string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go list -m: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// sanitize turns an import path into a safe temp filename.
func sanitize(importPath string) string {
	return strings.NewReplacer("/", "_", ".", "_").Replace(importPath)
}
