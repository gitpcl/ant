package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
)

// globalFlags builds a pflag set matching the CLI's precedence-bearing global
// flags so Bind tests exercise the real flag→key wiring.
func globalFlags(t *testing.T, set map[string]string) *pflag.FlagSet {
	t.Helper()
	fs := pflag.NewFlagSet("ant", pflag.ContinueOnError)
	fs.Int("concurrency", 0, "")
	fs.String("fixer", "", "")
	fs.String("model", "", "")
	for name, val := range set {
		if err := fs.Set(name, val); err != nil {
			t.Fatalf("set --%s: %v", name, val)
		}
	}
	return fs
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ant.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestBindFlagBeatsTomlEndToEnd proves the production Bind path (not just the
// test helper) layers a set flag over ant.toml with viper as the one authority.
func TestBindFlagBeatsTomlEndToEnd(t *testing.T) {
	path := writeConfig(t, "[colony]\nfixer = \"pi\"\n")
	v, _, err := Bind(globalFlags(t, map[string]string{"fixer": "claudecode"}), path)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	r := NewResolver(v, ManifestDefaults{})
	if got := r.Fixer(); got != "claudecode" {
		t.Errorf("flag must beat ant.toml end-to-end: Fixer() = %q, want claudecode", got)
	}
}

// TestBindUnsetFlagDoesNotClobberToml proves the subtle case: an UNSET --fixer
// (empty pflag default) must not override the ant.toml value. If viper let the
// empty default win, zero-flag runs would silently ignore the config.
func TestBindUnsetFlagDoesNotClobberToml(t *testing.T) {
	path := writeConfig(t, "[colony]\nfixer = \"pi\"\nconcurrency = 9\n")
	v, _, err := Bind(globalFlags(t, nil), path)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	r := NewResolver(v, ManifestDefaults{Fixer: strptr("manifest")})
	if got := r.Fixer(); got != "pi" {
		t.Errorf("unset flag must not clobber ant.toml: Fixer() = %q, want pi", got)
	}
	if got := r.Concurrency(); got != 9 {
		t.Errorf("ant.toml concurrency should win over default: %d, want 9", got)
	}
}

// TestBindZeroConfig proves a missing ant.toml is not an error and yields the
// built-in defaults through the resolver (bare `ant` works zero-config).
func TestBindZeroConfig(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.toml")
	v, warnings, err := Bind(globalFlags(t, nil), missing)
	if err != nil {
		t.Fatalf("missing config must not error: %v", err)
	}
	if warnings != nil {
		t.Errorf("missing config should produce no warnings, got %v", warnings)
	}
	r := NewResolver(v, ManifestDefaults{})
	if got := r.Fixer(); got != DefaultFixer {
		t.Errorf("zero-config Fixer() = %q, want default %q", got, DefaultFixer)
	}
}

// TestBindSurfacesUnknownKeyWarnings proves Bind relays the loader's unknown-key
// warnings so the CLI can print them (never a silent ignore).
func TestBindSurfacesUnknownKeyWarnings(t *testing.T) {
	path := writeConfig(t, "[colony]\nfixer = \"pi\"\nboguskey = 1\n")
	_, warnings, err := Bind(globalFlags(t, nil), path)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("Bind must surface unknown-key warnings")
	}
}

// TestBindMalformedIsOperational proves a broken ant.toml returned through Bind
// is the operational error the CLI maps to exit 2.
func TestBindMalformedIsOperational(t *testing.T) {
	path := writeConfig(t, "this = = broken ][")
	if _, _, err := Bind(globalFlags(t, nil), path); err == nil {
		t.Fatal("malformed config through Bind must error")
	}
}
