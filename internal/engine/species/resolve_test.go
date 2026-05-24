package species

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/gitpcl/ant/internal/engine/config"
)

// builtinFixtureFS is a tiny in-memory built-in tree: one species, "shared",
// with auto_apply=true. Resolution tests layer a user "shared" over it and an
// ant.toml override on top, exercising the merge without the real embed.
func builtinFixtureFS() fstest.MapFS {
	return fstest.MapFS{
		"shared/species.toml": &fstest.MapFile{Data: []byte(`
name        = "shared"
description = "built-in version"
severity    = "low"
auto_apply  = true

[detector]
kind = "ast-grep"
rule = "detect.yml"

[fix]
kind      = "deterministic"
transform = "delete-match"

[verify]
checks = ["compile", "detector-clears"]
`)},
		"shared/detect.yml": &fstest.MapFile{Data: []byte("id: shared\n")},
	}
}

// writeUserSpecies writes a user species folder under root/<name>/ and returns
// root. The user "shared" has description "user version" and auto_apply=false so
// shadowing is observable both by description and by the resolved auto_apply.
func writeUserSpecies(t *testing.T, name, manifest, rule string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir user species: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write user manifest: %v", err)
	}
	if rule != "" {
		if err := os.WriteFile(filepath.Join(dir, "detect.yml"), []byte(rule), 0o644); err != nil {
			t.Fatalf("write user rule: %v", err)
		}
	}
	return root
}

const userSharedManifest = `
name        = "shared"
description = "user version"
severity    = "high"
auto_apply  = false

[detector]
kind = "ast-grep"
rule = "detect.yml"

[fix]
kind      = "deterministic"
transform = "delete-match"

[verify]
checks = ["compile", "detector-clears"]
`

// TestResolution_UserShadowsBuiltin asserts a same-named user species overrides
// the built-in: the resolved "shared" is the user version (description, origin,
// and its lower auto_apply default all come from the user manifest).
func TestResolution_UserShadowsBuiltin(t *testing.T) {
	userRoot := writeUserSpecies(t, "shared", userSharedManifest, "id: shared\n")
	r := newResolverFS(builtinFixtureFS(), userRoot, NewRegistry())

	resolved, err := r.Resolve(config.Config{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("resolved %d species, want 1 (user shadows built-in of same name)", len(resolved))
	}
	got := resolved[0]
	if got.Origin != OriginUser {
		t.Errorf("Origin = %v, want user (user shadows built-in)", got.Origin)
	}
	if got.Manifest.Description != "user version" {
		t.Errorf("Description = %q, want %q (user manifest, not built-in)", got.Manifest.Description, "user version")
	}
	if got.EffectiveAutoApply {
		t.Errorf("EffectiveAutoApply = true, want false (user manifest auto_apply=false shadows built-in true)")
	}
}

// TestResolution_AutoApplyAntTomlOverManifest asserts effective auto_apply uses
// the ant.toml [species.<name>] value over the manifest default, in both
// directions: an ant.toml false beats a manifest true, and an ant.toml true
// beats a manifest false.
func TestResolution_AutoApplyAntTomlOverManifest(t *testing.T) {
	off := false
	on := true

	t.Run("ant.toml false beats manifest true", func(t *testing.T) {
		// Built-in "shared" has manifest auto_apply=true; no user species.
		r := newResolverFS(builtinFixtureFS(), "", NewRegistry())
		cfg := config.Config{Species: map[string]config.Species{
			"shared": {AutoApply: &off},
		}}
		resolved, err := r.Resolve(cfg)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if resolved[0].EffectiveAutoApply {
			t.Errorf("EffectiveAutoApply = true, want false (ant.toml override beats manifest true)")
		}
	})

	t.Run("ant.toml true beats manifest false", func(t *testing.T) {
		// User "shared" has manifest auto_apply=false; ant.toml flips it true.
		userRoot := writeUserSpecies(t, "shared", userSharedManifest, "id: shared\n")
		r := newResolverFS(builtinFixtureFS(), userRoot, NewRegistry())
		cfg := config.Config{Species: map[string]config.Species{
			"shared": {AutoApply: &on},
		}}
		resolved, err := r.Resolve(cfg)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if !resolved[0].EffectiveAutoApply {
			t.Errorf("EffectiveAutoApply = false, want true (ant.toml override beats manifest false)")
		}
	})
}

// TestResolution_EnabledAntTomlOverManifest asserts an ant.toml enabled override
// flips a manifest's enabled default (the ai-slop activation path).
func TestResolution_EnabledAntTomlOverManifest(t *testing.T) {
	on := true
	// A built-in shipped disabled (enabled=false), enabled via ant.toml.
	disabledFS := fstest.MapFS{
		"slop/species.toml": &fstest.MapFile{Data: []byte(`
name        = "slop"
description = "ships disabled"
severity    = "low"
enabled     = false

[detector]
kind = "ast-grep"
rule = "detect.yml"

[fix]
kind   = "llm"
prompt = "fix.md"

[verify]
checks = ["compile", "detector-clears"]
`)},
		"slop/detect.yml": &fstest.MapFile{Data: []byte("id: slop\n")},
		"slop/fix.md":     &fstest.MapFile{Data: []byte("fix it\n")},
	}
	r := newResolverFS(disabledFS, "", NewRegistry())

	// Default: disabled.
	resolved, err := r.Resolve(config.Config{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved[0].EffectiveEnabled {
		t.Errorf("EffectiveEnabled = true, want false (manifest enabled=false, no override)")
	}

	// ant.toml enables it.
	cfg := config.Config{Species: map[string]config.Species{"slop": {Enabled: &on}}}
	resolved, err = r.Resolve(cfg)
	if err != nil {
		t.Fatalf("Resolve with override: %v", err)
	}
	if !resolved[0].EffectiveEnabled {
		t.Errorf("EffectiveEnabled = false, want true (ant.toml enabled=true overrides manifest)")
	}
}

// TestResolution_MissingUserRootIsZeroConfig asserts a non-existent user root is
// not an error: resolution falls back to the built-in tree alone.
func TestResolution_MissingUserRootIsZeroConfig(t *testing.T) {
	r := newResolverFS(builtinFixtureFS(), filepath.Join(t.TempDir(), "does-not-exist"), NewRegistry())
	resolved, err := r.Resolve(config.Config{})
	if err != nil {
		t.Fatalf("Resolve with missing user root: %v", err)
	}
	if len(resolved) != 1 || resolved[0].Origin != OriginBuiltin {
		t.Errorf("resolved = %+v, want the single built-in species", resolved)
	}
}

// TestResolution_MalformedUserSpeciesRejected asserts a broken user manifest is
// surfaced as an error (not silently dropped), naming the species.
func TestResolution_MalformedUserSpeciesRejected(t *testing.T) {
	// llm fix with no prompt — invalid.
	bad := `
name        = "broken"
description = "no prompt for llm"
severity    = "low"

[detector]
kind = "ast-grep"
rule = "detect.yml"

[fix]
kind = "llm"

[verify]
checks = ["compile"]
`
	userRoot := writeUserSpecies(t, "broken", bad, "id: broken\n")
	r := newResolverFS(builtinFixtureFS(), userRoot, NewRegistry())
	_, err := r.Resolve(config.Config{})
	if err == nil {
		t.Fatal("Resolve = nil error, want rejection of malformed user species")
	}
	if !IsMalformed(err) {
		t.Errorf("error %v is not classified as malformed (ErrInvalidManifest)", err)
	}
}
