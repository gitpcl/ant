package species

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	builtins "github.com/gitpcl/ant/species"
)

// MaterializeBuiltinRules extracts the embedded built-in species tree
// (builtins.FS) to a fresh temp directory on disk and returns its root plus a
// cleanup func. It exists because detection is a shell-out plugin boundary
// (TECHSPEC §2): the ast-grep adapter runs the external binary against a rule
// FILE, but built-in rules ship embedded in the binary (species/embed.go,
// go:embed). Materializing the tree once per run bridges the two — the CLI points
// the detector's rulesRoot at the returned directory so `ant scout` / `ant fix`
// resolve `unused-import/detect.yml` to a real on-disk file.
//
// Only the embedded tree is written (the binary's own built-ins); user species
// already live on disk and are read in place. The caller defers cleanup to remove
// the temp tree when the run ends. A write failure is returned (the CLI maps it to
// an operational error) rather than silently leaving scout with unresolvable
// rules.
func MaterializeBuiltinRules() (root string, cleanup func(), err error) {
	return materializeFS(builtins.FS())
}

// materializeFS is the testable core: it copies every regular file in fsys into a
// new temp dir, preserving the relative layout, and returns the dir + cleanup.
// Split from MaterializeBuiltinRules so a test can drive it with an arbitrary FS
// without touching the real embed.
func materializeFS(fsys fs.FS) (string, func(), error) {
	dir, err := os.MkdirTemp("", "ant-builtin-rules-")
	if err != nil {
		return "", func() {}, fmt.Errorf("species: create rules dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	walkErr := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		target := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil // skip non-regular entries defensively
		}
		data, readErr := fs.ReadFile(fsys, p)
		if readErr != nil {
			return readErr
		}
		if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
			return mkErr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if walkErr != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("species: materialize built-in rules: %w", walkErr)
	}
	return dir, cleanup, nil
}
