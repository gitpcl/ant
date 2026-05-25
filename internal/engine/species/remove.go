package species

// remove.go is the engine side of `ant species remove <name>` (TECHSPEC §7). It
// deletes an installed community species from the on-disk .ant/species/ tree and
// clears its persisted trust state, so a later reinstall is treated as freshly
// installed again (re-armed propose-only, TECHSPEC §6.3). Built-in (embedded)
// species are PROTECTED: they ship in the binary and cannot be removed from disk,
// so removing one is refused with a clear error and nothing is touched.
//
// The filesystem delete + trust clear live here (not in cmd/ant) because they are
// persistence/business logic the boundary test keeps out of the thin CLI front
// door. The CLI handler only resolves the destination root and the trust seam and
// delegates.

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gitpcl/ant/internal/engine"
	builtins "github.com/gitpcl/ant/species"
)

// TrustClearer is the narrow persistence seam Remove uses to drop a species'
// tracked install/review state. It is defined here (where it is used) so the
// remove feature owns its own contract; the local *store.Store satisfies it via
// ClearTrust, and tests inject a fake. Keeping it small (one method) follows the
// "accept small interfaces" Go idiom and keeps the dependency pointing
// store → species, never the reverse.
type TrustClearer interface {
	// ClearTrust removes a species' tracked trust state entirely. It is a no-op
	// for an untracked species (so removing a never-run species is not an error).
	ClearTrust(name string) error
}

// RemoveOptions parameterizes a remove.
type RemoveOptions struct {
	// Name is the species name == its on-disk folder name under UserRoot. A name
	// that is empty, blank, or contains a path separator or ".." is refused (it
	// could otherwise escape UserRoot).
	Name string
	// UserRoot is the on-disk .ant/species directory the installed folder lives
	// under (typically "<repo>/.ant/species").
	UserRoot string
	// Trust is the persistence seam whose tracked state for Name is cleared after
	// the folder is deleted. A nil Trust skips the clear (the folder is still
	// removed) — but callers should pass the real store so a reinstall re-arms the
	// freshly-installed override.
	Trust TrustClearer
}

// Remove deletes the installed species folder UserRoot/<Name> and clears its
// persisted trust state. It enforces three guards before touching disk:
//
//   - The name must be a single safe path element (no separators, no "..", not
//     empty/blank) so it cannot escape UserRoot.
//   - A built-in (embedded) species name is REFUSED: built-ins ship in the binary
//     and are not on disk; removing one is a user error, not a delete. This check
//     uses the embedded tree, not disk presence, so a built-in is protected even
//     if a same-named folder happens to exist under UserRoot.
//   - The folder must actually exist under UserRoot; removing a non-existent
//     installed species is a clear error, not a silent no-op.
//
// All failures wrap engine.ErrOperational so the CLI maps them to exit code 2.
// The trust state is cleared only AFTER the folder is gone, so a partial failure
// never leaves "trust cleared but folder still present".
func Remove(opts RemoveOptions) error {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return fmt.Errorf("%w: species remove: a species name is required", engine.ErrOperational)
	}
	if !isSafeName(name) {
		return fmt.Errorf("%w: species remove: invalid species name %q", engine.ErrOperational, opts.Name)
	}
	if opts.UserRoot == "" {
		return fmt.Errorf("%w: species remove: user species root is required", engine.ErrOperational)
	}

	// Built-in protection: refuse before any disk work. Built-ins are vetted at
	// release time and live in the binary; they have no removable on-disk folder.
	if isBuiltinName(name) {
		return fmt.Errorf("%w: species remove: %q is a built-in species and cannot be removed", engine.ErrOperational, name)
	}

	dir := filepath.Join(opts.UserRoot, name)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("%w: species remove: no installed species %q under %s", engine.ErrOperational, name, opts.UserRoot)
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("%w: species remove: delete %q: %v", engine.ErrOperational, dir, err)
	}

	// Clear trust LAST: the folder is gone, so a reinstall is treated as fresh
	// again (forced propose-only) rather than inheriting stale "reviewed" trust.
	if opts.Trust != nil {
		if err := opts.Trust.ClearTrust(name); err != nil {
			return fmt.Errorf("%w: species remove: clear trust state for %q: %v", engine.ErrOperational, name, err)
		}
	}
	return nil
}

// isSafeName reports whether name is a single, contained path element safe to
// join under the user root: no path separators, no "..", not ".". This mirrors
// the containment discipline install uses for untrusted paths, applied here to a
// user-supplied name so `remove ../../etc` can never escape .ant/species.
func isSafeName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, filepath.Separator) {
		return false
	}
	// A name that does not survive a clean as itself (e.g. embeds "..") is unsafe.
	return filepath.Clean(name) == name && !strings.Contains(name, "..")
}

// isBuiltinName reports whether name is one of the embedded built-in species. A
// built-in is a directory directly under the embedded tree containing a
// species.toml — the same identification rule the resolver's discover uses, read
// from builtins.FS() so the protected set is exactly what ships in the binary
// (no duplicated hard-coded list to drift out of sync).
func isBuiltinName(name string) bool {
	bfs := builtins.FS()
	if _, err := fs.Stat(bfs, path.Join(name, ManifestFileName)); err == nil {
		return true
	}
	return false
}
