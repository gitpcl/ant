package species

import (
	"testing"

	"github.com/gitpcl/ant/internal/engine/config"
	builtins "github.com/gitpcl/ant/species"
)

// adr0002 pins each built-in species' fix strategy and trust defaults from
// ADR-0002 (docs/decisions/0002-launch-species.md). The embed test asserts the
// embedded manifests match this table exactly, so a drift in any built-in
// manifest is caught here.
var adr0002 = map[string]struct {
	fixKind   string
	autoApply bool
	enabled   bool
}{
	"unused-import": {FixKindDeterministic, true, true},
	"dead-code":     {FixKindDeterministic, true, true},
	"n+1-query":     {FixKindLLM, false, true},
	"missing-await": {FixKindLLM, false, true},
	"nil-deref":     {FixKindLLM, false, true},
	"ai-slop":       {FixKindLLM, false, false},
}

// TestEmbed_BuiltinsDiscoverableNoDisk is the core feature-3 assertion: the six
// built-in species load from the EMBEDDED FS with no on-disk species/ directory
// present at runtime. The resolver is given an empty userRoot, so the only
// source is builtins.FS().
func TestEmbed_BuiltinsDiscoverableNoDisk(t *testing.T) {
	// userRoot "" => the resolver never touches the disk; built-ins come purely
	// from the embedded tree compiled into the test binary.
	r := NewResolver("", NewRegistry())
	resolved, err := r.Resolve(config.Config{})
	if err != nil {
		t.Fatalf("Resolve built-ins from embed: %v", err)
	}

	got := map[string]Resolved{}
	for _, rv := range resolved {
		got[rv.Manifest.Name] = rv
		if rv.Origin != OriginBuiltin {
			t.Errorf("%s: Origin = %v, want builtin", rv.Manifest.Name, rv.Origin)
		}
	}

	if len(got) != len(adr0002) {
		t.Fatalf("resolved %d built-in species, want %d: got %v", len(got), len(adr0002), keysOf(got))
	}

	for name, want := range adr0002 {
		rv, ok := got[name]
		if !ok {
			t.Errorf("built-in species %q missing from embedded tree", name)
			continue
		}
		if rv.Manifest.Fix.Kind != want.fixKind {
			t.Errorf("%s: fix kind = %q, want %q (ADR-0002)", name, rv.Manifest.Fix.Kind, want.fixKind)
		}
		if rv.EffectiveAutoApply != want.autoApply {
			t.Errorf("%s: effective auto_apply = %v, want %v (ADR-0002, no ant.toml override)", name, rv.EffectiveAutoApply, want.autoApply)
		}
		if rv.EffectiveEnabled != want.enabled {
			t.Errorf("%s: effective enabled = %v, want %v (ADR-0002)", name, rv.EffectiveEnabled, want.enabled)
		}
	}
}

// TestEmbed_FSContainsManifests is a lower-level guard that the embed directive
// actually captured each species.toml, independent of the resolver.
func TestEmbed_FSContainsManifests(t *testing.T) {
	fsys := builtins.FS()
	for name := range adr0002 {
		manifestPath := name + "/" + ManifestFileName
		if _, err := fsys.Open(manifestPath); err != nil {
			t.Errorf("embedded FS missing %s: %v", manifestPath, err)
		}
	}
}

func keysOf(m map[string]Resolved) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
