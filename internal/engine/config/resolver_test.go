package config

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// newBoundViper mimics the CLI's wiring: a pflag FlagSet matching the global
// flags, optionally with some flags "set" on the command line, bound into a
// fresh viper under the dotted config keys. This is the exact precedence surface
// the CLI hands the resolver, so resolver tests exercise the real layering — not
// a mock of it. tomlColony, when non-nil, is merged into viper's config-file
// band (the layer that sits below flags and above defaults).
func newBoundViper(t *testing.T, setFlags map[string]string, tomlColony map[string]any) *viper.Viper {
	t.Helper()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.Int("concurrency", 0, "")
	fs.String("fixer", "", "")
	fs.String("model", "", "")
	for name, val := range setFlags {
		if err := fs.Set(name, val); err != nil {
			t.Fatalf("set flag --%s=%s: %v", name, val, err)
		}
	}
	v := viper.New()
	mustBind(t, v, KeyConcurrency, fs.Lookup("concurrency"))
	mustBind(t, v, KeyFixer, fs.Lookup("fixer"))
	mustBind(t, v, KeyModel, fs.Lookup("model"))
	if tomlColony != nil {
		v.SetConfigType("toml")
		if err := v.MergeConfigMap(map[string]any{"colony": tomlColony}); err != nil {
			t.Fatalf("merge toml map: %v", err)
		}
	}
	return v
}

func mustBind(t *testing.T, v *viper.Viper, key string, f *pflag.Flag) {
	t.Helper()
	if err := v.BindPFlag(key, f); err != nil {
		t.Fatalf("bind %s: %v", key, err)
	}
}

// TestFlagOverridesToml is the approach-gate validation (recorded in
// progress_log.md): prove a flag value beats an ant.toml value with viper as the
// single precedence authority BEFORE wiring the manifest/default layers. If this
// fails, the "viper owns precedence" approach is wrong and we switch to the
// alternative — so it runs first.
func TestFlagOverridesToml(t *testing.T) {
	v := newBoundViper(t,
		map[string]string{"fixer": "claudecode"}, // flag set on the command line
		map[string]any{"fixer": "pi"},            // [colony].fixer in ant.toml
	)
	r := NewResolver(v, ManifestDefaults{})
	if got := r.Fixer(); got != "claudecode" {
		t.Fatalf("flag must override ant.toml: Fixer() = %q, want %q", got, "claudecode")
	}
}

// strptr / intptr build pointers to literals for the optional manifest fields.
func strptr(s string) *string { return &s }
func intptr(n int) *int       { return &n }

// TestTomlOverridesManifest proves the middle precedence band: with no flag set,
// an ant.toml value wins over the species-manifest value. An unset bound flag
// (empty pflag default) must NOT clobber the config-file layer.
func TestTomlOverridesManifest(t *testing.T) {
	v := newBoundViper(t,
		nil,                           // no flags set
		map[string]any{"fixer": "pi"}, // ant.toml [colony].fixer
	)
	r := NewResolver(v, ManifestDefaults{Fixer: strptr("manifest-fixer")})
	if got := r.Fixer(); got != "pi" {
		t.Fatalf("ant.toml must override manifest: Fixer() = %q, want %q", got, "pi")
	}
}

// TestManifestOverridesDefault proves the lowest contested band: with no flag
// and no ant.toml value, the species manifest wins over the built-in default.
func TestManifestOverridesDefault(t *testing.T) {
	v := newBoundViper(t, nil, nil) // nothing set
	r := NewResolver(v, ManifestDefaults{Fixer: strptr("manifest-fixer")})
	if got := r.Fixer(); got != "manifest-fixer" {
		t.Fatalf("manifest must override built-in default: Fixer() = %q, want %q", got, "manifest-fixer")
	}
}

// TestDefaultWhenNothingSet proves the floor: no flag, no ant.toml, no manifest
// → the built-in default (TECHSPEC §9).
func TestDefaultWhenNothingSet(t *testing.T) {
	v := newBoundViper(t, nil, nil)
	r := NewResolver(v, ManifestDefaults{})
	if got := r.Fixer(); got != DefaultFixer {
		t.Errorf("Fixer() = %q, want built-in default %q", got, DefaultFixer)
	}
	if got := r.Model(); got != DefaultModel {
		t.Errorf("Model() = %q, want built-in default %q", got, DefaultModel)
	}
	if got, want := r.Concurrency(), DefaultConcurrency(); got != want {
		t.Errorf("Concurrency() = %d, want NumCPU default %d", got, want)
	}
}

// TestConcurrencyPrecedence walks the int-valued knob through every band so the
// zero-flag-means-unset rule (a 0 --concurrency must not clobber config) is
// proven, not assumed.
func TestConcurrencyPrecedence(t *testing.T) {
	t.Run("flag wins", func(t *testing.T) {
		v := newBoundViper(t, map[string]string{"concurrency": "12"}, map[string]any{"concurrency": int64(4)})
		r := NewResolver(v, ManifestDefaults{Concurrency: intptr(2)})
		if got := r.Concurrency(); got != 12 {
			t.Fatalf("Concurrency() = %d, want 12 (flag wins)", got)
		}
	})
	t.Run("toml beats manifest", func(t *testing.T) {
		v := newBoundViper(t, nil, map[string]any{"concurrency": int64(4)})
		r := NewResolver(v, ManifestDefaults{Concurrency: intptr(2)})
		if got := r.Concurrency(); got != 4 {
			t.Fatalf("Concurrency() = %d, want 4 (toml beats manifest)", got)
		}
	})
	t.Run("manifest beats default", func(t *testing.T) {
		v := newBoundViper(t, nil, nil)
		r := NewResolver(v, ManifestDefaults{Concurrency: intptr(2)})
		if got := r.Concurrency(); got != 2 {
			t.Fatalf("Concurrency() = %d, want 2 (manifest beats NumCPU)", got)
		}
	})
}
