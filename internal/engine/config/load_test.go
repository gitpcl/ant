package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
)

// TestConfigSchemaParsesEveryField parses the sample ant.toml (TECHSPEC §9) and
// asserts every documented field is read into the typed Config: the [colony]
// knobs, the [ignore] paths, and each [species.<name>] override reachable by
// name.
func TestConfigSchemaParsesEveryField(t *testing.T) {
	cfg, found, err := Load(filepath.Join("testdata", "ant.toml"))
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if !found {
		t.Fatal("Load: sample ant.toml should be found")
	}

	// [colony]
	if cfg.Colony.Concurrency == nil || *cfg.Colony.Concurrency != 6 {
		t.Errorf("colony.concurrency = %v, want 6", cfg.Colony.Concurrency)
	}
	if cfg.Colony.Fixer == nil || *cfg.Colony.Fixer != "pi" {
		t.Errorf("colony.fixer = %v, want \"pi\"", cfg.Colony.Fixer)
	}
	if cfg.Colony.Model == nil || *cfg.Colony.Model != "qwen2.5-coder" {
		t.Errorf("colony.model = %v, want \"qwen2.5-coder\"", cfg.Colony.Model)
	}
	if cfg.Colony.Trails == nil || *cfg.Colony.Trails != true {
		t.Errorf("colony.trails = %v, want true (ADR-0003 opt-in key parses)", cfg.Colony.Trails)
	}

	// [ignore]
	wantIgnore := []string{"vendor/", "node_modules/", "*_generated.go"}
	if got := cfg.Ignore.Paths; !equalStrings(got, wantIgnore) {
		t.Errorf("ignore.paths = %v, want %v", got, wantIgnore)
	}

	// [telemetry] — the opt-in flag parses (default is off; the sample sets it on).
	if cfg.Telemetry.Enabled == nil || *cfg.Telemetry.Enabled != true {
		t.Errorf("telemetry.enabled = %v, want true (PRD §8 opt-in key parses)", cfg.Telemetry.Enabled)
	}

	// [species.<name>] — reachable by species name.
	ui, ok := cfg.SpeciesConfig("unused-import")
	if !ok {
		t.Fatal("species.unused-import section should be present")
	}
	if ui.AutoApply == nil || *ui.AutoApply != true {
		t.Errorf("species.unused-import.auto_apply = %v, want true", ui.AutoApply)
	}

	nq, ok := cfg.SpeciesConfig("n+1-query")
	if !ok {
		t.Fatal("species.n+1-query section should be present")
	}
	if nq.AutoApply == nil || *nq.AutoApply != false {
		t.Errorf("species.n+1-query.auto_apply = %v, want false", nq.AutoApply)
	}

	slop, ok := cfg.SpeciesConfig("ai-slop")
	if !ok {
		t.Fatal("species.ai-slop section should be present")
	}
	if slop.Enabled == nil || *slop.Enabled != false {
		t.Errorf("species.ai-slop.enabled = %v, want false", slop.Enabled)
	}
}

// TestResolveTrails asserts the flag > ant.toml > default-off resolution chain
// for the trails opt-in (ADR-0003). Default is OFF; an explicit flag wins over
// ant.toml in both directions, proving the flag can force trails on OR off.
func TestResolveTrails(t *testing.T) {
	on, off := true, false

	t.Run("default off (no flag, no toml)", func(t *testing.T) {
		var cfg Config
		if cfg.ResolveTrails(false, false) {
			t.Error("ResolveTrails default = true, want false (v1 ships embarrassingly-parallel)")
		}
	})
	t.Run("toml on, no flag", func(t *testing.T) {
		cfg := Config{Colony: Colony{Trails: &on}}
		if !cfg.ResolveTrails(false, false) {
			t.Error("ResolveTrails = false, want true ([colony] trails=true with no flag)")
		}
	})
	t.Run("toml off, no flag", func(t *testing.T) {
		cfg := Config{Colony: Colony{Trails: &off}}
		if cfg.ResolveTrails(false, false) {
			t.Error("ResolveTrails = true, want false ([colony] trails=false)")
		}
	})
	t.Run("flag on beats toml off", func(t *testing.T) {
		cfg := Config{Colony: Colony{Trails: &off}}
		if !cfg.ResolveTrails(true, true) {
			t.Error("ResolveTrails = false, want true (--trails beats ant.toml trails=false)")
		}
	})
	t.Run("flag off beats toml on", func(t *testing.T) {
		cfg := Config{Colony: Colony{Trails: &on}}
		if cfg.ResolveTrails(true, false) {
			t.Error("ResolveTrails = true, want false (--trails=false beats ant.toml trails=true)")
		}
	})
}

// TestResolveTelemetry asserts the ant.toml > default-off resolution for the
// telemetry opt-in (PRD §8). Default is OFF (zero collection); an explicit
// [telemetry] enabled key wins in both directions. There is intentionally no
// flag override — telemetry is a deliberate, persistent opt-in, never flipped on
// by a transient command-line flag.
func TestResolveTelemetry(t *testing.T) {
	on, off := true, false

	t.Run("default off (no toml)", func(t *testing.T) {
		var cfg Config
		if cfg.ResolveTelemetry() {
			t.Error("ResolveTelemetry default = true, want false (privacy is the contract; opt-in only)")
		}
	})
	t.Run("toml on", func(t *testing.T) {
		cfg := Config{Telemetry: Telemetry{Enabled: &on}}
		if !cfg.ResolveTelemetry() {
			t.Error("ResolveTelemetry = false, want true ([telemetry] enabled=true)")
		}
	})
	t.Run("toml explicit off", func(t *testing.T) {
		cfg := Config{Telemetry: Telemetry{Enabled: &off}}
		if cfg.ResolveTelemetry() {
			t.Error("ResolveTelemetry = true, want false ([telemetry] enabled=false)")
		}
	})
}

// TestUnknownKeysAreWarnings asserts an unrecognized key (a typo, an unknown
// section) produces a warning — not a silent ignore and not a hard error — while
// the recognized keys still load (TECHSPEC §9 acceptance).
func TestUnknownKeysAreWarnings(t *testing.T) {
	cfg, warnings, found, err := LoadStrict(filepath.Join("testdata", "ant-unknown.toml"))
	if err != nil {
		t.Fatalf("LoadStrict: unknown keys must not be a hard error, got: %v", err)
	}
	if !found {
		t.Fatal("file should be found")
	}
	if len(warnings) == 0 {
		t.Fatal("unknown keys must produce warnings, got none (silent ignore is forbidden)")
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"fixxer", "bogus"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings should name unknown key %q; got:\n%s", want, joined)
		}
	}
	// Recognized keys still load despite the unknown ones.
	if cfg.Colony.Concurrency == nil || *cfg.Colony.Concurrency != 4 {
		t.Errorf("recognized colony.concurrency should still load = %v, want 4", cfg.Colony.Concurrency)
	}
	if _, ok := cfg.SpeciesConfig("unused-import"); !ok {
		t.Error("recognized species.unused-import should still load")
	}
}

// TestMissingFileIsNotAnError asserts zero-config: a missing ant.toml is not an
// error (bare `ant` must work with no config), returning found=false.
func TestMissingFileIsNotAnError(t *testing.T) {
	cfg, found, err := Load(filepath.Join("testdata", "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("missing file must not error (zero-config): %v", err)
	}
	if found {
		t.Error("found should be false for a missing file")
	}
	if cfg.Species != nil {
		t.Error("missing file should yield the zero Config")
	}
}

// TestMalformedConfigIsOperational asserts a present-but-broken ant.toml is an
// operational error (exit 2) that classifies via engine.ErrOperational, so the
// CLI maps it to the right code without importing this package's internals.
func TestMalformedConfigIsOperational(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "ant.toml")
	if err := os.WriteFile(bad, []byte("this is = = not valid toml ]["), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	_, found, err := Load(bad)
	if err == nil {
		t.Fatal("malformed config must be an error")
	}
	if !found {
		t.Error("a present-but-broken file should report found=true")
	}
	if !errors.Is(err, engine.ErrOperational) {
		t.Errorf("malformed config must classify as operational (exit 2): %v", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
