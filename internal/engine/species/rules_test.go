package species

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// TestMaterializeFSWritesTreeToDisk verifies the embedded-rules materializer
// reproduces an FS's regular files on disk under a temp root, preserving the
// nested layout — the bridge from the go:embed species tree to the shell-out
// ast-grep adapter (TECHSPEC §2).
func TestMaterializeFSWritesTreeToDisk(t *testing.T) {
	src := fstest.MapFS{
		"unused-import/species.toml": {Data: []byte("name = \"unused-import\"\n")},
		"unused-import/detect.yml":   {Data: []byte("id: unused-import\n")},
		"dead-code/detect.yml":       {Data: []byte("id: dead-code\n")},
	}

	root, cleanup, err := materializeFS(src)
	if err != nil {
		t.Fatalf("materializeFS: %v", err)
	}
	defer cleanup()

	for name, want := range map[string]string{
		"unused-import/species.toml": "name = \"unused-import\"\n",
		"unused-import/detect.yml":   "id: unused-import\n",
		"dead-code/detect.yml":       "id: dead-code\n",
	} {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Errorf("read materialized %s: %v", name, err)
			continue
		}
		if string(got) != want {
			t.Errorf("materialized %s = %q, want %q", name, got, want)
		}
	}

	// Cleanup must remove the temp tree so a run leaves no rule files behind.
	cleanup()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("cleanup did not remove %s (stat err: %v)", root, err)
	}
}

// TestMaterializeBuiltinRulesEmitsEmbeddedSpecies verifies the production entry
// point extracts the real embedded tree: every built-in species folder lands on
// disk with its detect.yml, so the CLI can point ast-grep at <root>/<species>/detect.yml.
func TestMaterializeBuiltinRulesEmitsEmbeddedSpecies(t *testing.T) {
	root, cleanup, err := MaterializeBuiltinRules()
	if err != nil {
		t.Fatalf("MaterializeBuiltinRules: %v", err)
	}
	defer cleanup()

	for _, name := range []string{"unused-import", "dead-code"} {
		rule := filepath.Join(root, name, "detect.yml")
		if _, err := os.Stat(rule); err != nil {
			t.Errorf("embedded rule %s not materialized: %v", rule, err)
		}
	}
}
