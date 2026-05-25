package species

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
)

// fakeTrustClearer records ClearTrust calls so a test can assert remove clears
// the persisted state (so a reinstall re-arms the freshly-installed override).
// It satisfies the small TrustClearer seam Remove accepts.
type fakeTrustClearer struct {
	cleared []string
	err     error
}

func (f *fakeTrustClearer) ClearTrust(name string) error {
	f.cleared = append(f.cleared, name)
	return f.err
}

// writeInstalledSpecies places a minimal well-formed installed species under
// userRoot/<name> (manifest + the rule file it references) so Remove has a real
// folder to delete. It mirrors the structure the loader validates.
func writeInstalledSpecies(t *testing.T, userRoot, name string) string {
	t.Helper()
	dir := filepath.Join(userRoot, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(validManifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "detect.yml"), []byte("id: demo\n"), 0o644); err != nil {
		t.Fatalf("write rule: %v", err)
	}
	return dir
}

// TestRemoveDeletesInstalledAndClearsTrust is the GREEN path: an installed
// species folder is deleted and its trust state cleared.
func TestRemoveDeletesInstalledAndClearsTrust(t *testing.T) {
	userRoot := filepath.Join(t.TempDir(), ".ant", "species")
	dir := writeInstalledSpecies(t, userRoot, "demo")
	clearer := &fakeTrustClearer{}

	if err := Remove(RemoveOptions{Name: "demo", UserRoot: userRoot, Trust: clearer}); err != nil {
		t.Fatalf("Remove: unexpected error: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("species folder still present after remove: stat err = %v", err)
	}
	if len(clearer.cleared) != 1 || clearer.cleared[0] != "demo" {
		t.Errorf("ClearTrust calls = %v, want [demo]", clearer.cleared)
	}
}

// TestRemoveRefusesBuiltin protects the embedded set: a built-in name is
// rejected with an operational error and nothing is deleted/cleared. We pass a
// user root that does NOT contain the name, proving Remove decides "built-in"
// from the embedded tree, not from disk presence.
func TestRemoveRefusesBuiltin(t *testing.T) {
	userRoot := filepath.Join(t.TempDir(), ".ant", "species")
	clearer := &fakeTrustClearer{}

	err := Remove(RemoveOptions{Name: "unused-import", UserRoot: userRoot, Trust: clearer})
	if err == nil {
		t.Fatal("Remove(built-in): expected an error, got nil")
	}
	if !errors.Is(err, engine.ErrOperational) {
		t.Errorf("Remove(built-in): error must wrap ErrOperational for exit 2, got %v", err)
	}
	if len(clearer.cleared) != 0 {
		t.Errorf("Remove(built-in): must not clear trust, cleared = %v", clearer.cleared)
	}
}

// TestRemoveMissingInstalled rejects removing a species that is neither a
// built-in nor installed on disk, with a clear operational error and no trust
// clear.
func TestRemoveMissingInstalled(t *testing.T) {
	userRoot := filepath.Join(t.TempDir(), ".ant", "species")
	if err := os.MkdirAll(userRoot, 0o755); err != nil {
		t.Fatalf("mkdir userRoot: %v", err)
	}
	clearer := &fakeTrustClearer{}

	err := Remove(RemoveOptions{Name: "ghost", UserRoot: userRoot, Trust: clearer})
	if err == nil {
		t.Fatal("Remove(missing): expected an error, got nil")
	}
	if !errors.Is(err, engine.ErrOperational) {
		t.Errorf("Remove(missing): error must wrap ErrOperational, got %v", err)
	}
	if len(clearer.cleared) != 0 {
		t.Errorf("Remove(missing): must not clear trust, cleared = %v", clearer.cleared)
	}
}

// TestRemoveContainsName guards against a crafted name escaping the user root
// via path separators / traversal: a name containing a separator or ".." is
// refused before any delete.
func TestRemoveContainsName(t *testing.T) {
	userRoot := filepath.Join(t.TempDir(), ".ant", "species")
	clearer := &fakeTrustClearer{}
	for _, bad := range []string{"../escape", "a/b", ".", "", "  "} {
		if err := Remove(RemoveOptions{Name: bad, UserRoot: userRoot, Trust: clearer}); err == nil {
			t.Errorf("Remove(%q): expected rejection of unsafe name, got nil", bad)
		}
	}
	if len(clearer.cleared) != 0 {
		t.Errorf("unsafe names must not clear trust, cleared = %v", clearer.cleared)
	}
}
