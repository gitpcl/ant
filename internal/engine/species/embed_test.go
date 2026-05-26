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
	"unused-import":          {FixKindDeterministic, true, true},
	"dead-code":              {FixKindDeterministic, true, true},
	"unused-variable":        {FixKindDeterministic, true, true},
	"redundant-conversion":   {FixKindDeterministic, true, true},
	"unreachable-code":       {FixKindDeterministic, true, true},
	"empty-block":            {FixKindDeterministic, false, true},
	"duplicate-condition":    {FixKindDeterministic, false, true},
	"redundant-nil-check":    {FixKindDeterministic, false, true},
	"ineffective-assignment": {FixKindDeterministic, false, true},
	"formatter-drift":        {FixKindTool, true, true},
	"import-sort":            {FixKindTool, true, true},
	"lint-autofix":           {FixKindTool, true, true},
	"trailing-debug-code":    {FixKindDeterministic, false, true},
	"n+1-query":              {FixKindLLM, false, true},
	"missing-await":          {FixKindLLM, false, true},
	"nil-deref":              {FixKindLLM, false, true},
	"ai-slop":                {FixKindLLM, false, false},
}

// TestEmbed_BuiltinsDiscoverableNoDisk is the core feature-3 assertion: the
// built-in species load from the EMBEDDED FS with no on-disk species/ directory
// present at runtime. The resolver is given an empty userRoot, so the only
// source is builtins.FS(). The expected set is the adr0002 table above.
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

// TestAISlopShipsDisabled is the ai-slop feature's core assertion against the
// REAL embedded species (not a synthetic fixture): on a default run (no ant.toml
// opt-in) the embedded ai-slop resolves DISABLED, so the colony's recipe builder
// excludes it (it cannot run); and an explicit ant.toml [species.ai-slop]
// enabled=true flips EffectiveEnabled on, the only path that activates it
// (ADR-0002 — the fuzzy classifier ships off, opt-in only). The other five
// built-ins stay enabled by default throughout, so enabling/disabling ai-slop
// is strictly per-species (no global switch).
func TestAISlopShipsDisabled(t *testing.T) {
	r := NewResolver("", NewRegistry())

	// 1. Default run: ai-slop is disabled, every other built-in is enabled.
	def, err := r.Resolve(config.Config{})
	if err != nil {
		t.Fatalf("Resolve default: %v", err)
	}
	for _, rv := range def {
		want := rv.Manifest.Name != "ai-slop" // all but ai-slop ship enabled
		if rv.EffectiveEnabled != want {
			t.Errorf("default run: %s EffectiveEnabled = %v, want %v", rv.Manifest.Name, rv.EffectiveEnabled, want)
		}
	}

	// 2. Opt-in: ant.toml [species.ai-slop] enabled = true activates it, and only
	// it — the other species are untouched by the override.
	on := true
	enabled, err := r.Resolve(config.Config{Species: map[string]config.Species{
		"ai-slop": {Enabled: &on},
	}})
	if err != nil {
		t.Fatalf("Resolve with ai-slop enabled: %v", err)
	}
	for _, rv := range enabled {
		if !rv.EffectiveEnabled {
			t.Errorf("with ai-slop opt-in: %s EffectiveEnabled = false, want true (every species, incl. opted-in ai-slop, is now enabled)", rv.Manifest.Name)
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
