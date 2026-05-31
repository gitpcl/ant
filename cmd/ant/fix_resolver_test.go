package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gitpcl/ant/internal/engine/config"
	"github.com/spf13/pflag"
)

// globalFlagSet mirrors the global pflags root.go registers, so config.Bind sees
// the exact flag surface runFix hands it. No flags are set here, so the test
// isolates the ant.toml band: every effective value comes from the file (or the
// built-in default when the file omits the knob).
func globalFlagSet() *pflag.FlagSet {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("fixer", "", "")
	fs.String("model", "", "")
	fs.Int("concurrency", 0, "")
	return fs
}

// resolveFor writes ant.toml content to a temp dir and resolves it through the
// SAME path runFix uses (config.Bind over the global flags + config.NewResolver),
// returning the resolver whose Model/Concurrency/MaxChanged* feed the colony and
// verify layers (cmd/ant/fix.go: RecipeConfig.Limits, RawModelModel, and
// DriveOptions.Concurrency).
func resolveFor(t *testing.T, toml string) *config.Resolver {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ant.toml")
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatalf("write ant.toml: %v", err)
	}
	v, _, err := config.Bind(globalFlagSet(), path)
	if err != nil {
		t.Fatalf("config.Bind: %v", err)
	}
	return config.NewResolver(v, config.ManifestDefaults{})
}

// TestFixResolverHonorsEveryAntTomlKnob is the Sprint 022 fix-uses-config-resolver
// acceptance test: it flips each of [colony].model, [colony].concurrency, and
// [verify].max_changed_lines/max_changed_files in ant.toml and asserts the
// effective value reaching the colony/verify layer (the resolver runFix reads)
// changed away from the built-in default for each knob. verify.DefaultLimits() is
// no longer called in runFix; these limits now come from the resolver below.
func TestFixResolverHonorsEveryAntTomlKnob(t *testing.T) {
	// Baseline: an empty config resolves to the built-in defaults. Each knob's
	// flipped value below is chosen to differ from these, so "changed" is provable.
	base := resolveFor(t, "")
	if base.Model() != config.DefaultModel {
		t.Fatalf("baseline Model() = %q, want default %q", base.Model(), config.DefaultModel)
	}
	if base.Concurrency() != config.DefaultConcurrency() {
		t.Fatalf("baseline Concurrency() = %d, want default %d", base.Concurrency(), config.DefaultConcurrency())
	}
	if base.MaxChangedLines() != config.DefaultMaxChangedLines {
		t.Fatalf("baseline MaxChangedLines() = %d, want default %d", base.MaxChangedLines(), config.DefaultMaxChangedLines)
	}
	if base.MaxChangedFiles() != config.DefaultMaxChangedFiles {
		t.Fatalf("baseline MaxChangedFiles() = %d, want default %d", base.MaxChangedFiles(), config.DefaultMaxChangedFiles)
	}

	// A single ant.toml that flips all four knobs at once. The values are picked to
	// be distinct from every built-in default (model != qwen2.5-coder, concurrency
	// != NumCPU under any plausible host, limits != 200/10).
	const flipped = `[colony]
model = "flipped-model"
concurrency = 1

[verify]
max_changed_lines = 7
max_changed_files = 3
`
	r := resolveFor(t, flipped)

	t.Run("colony.model reaches RawModelModel", func(t *testing.T) {
		if got := r.Model(); got != "flipped-model" {
			t.Errorf("Model() = %q, want %q (the value runFix feeds RecipeConfig.RawModelModel)", got, "flipped-model")
		}
		if r.Model() == config.DefaultModel {
			t.Errorf("Model() did not change from the built-in default")
		}
	})

	t.Run("colony.concurrency reaches the worker pool", func(t *testing.T) {
		// 1 is distinct from NumCPU on the CI host (which has >1 core); guard anyway.
		if config.DefaultConcurrency() == 1 {
			t.Skip("host NumCPU == 1; concurrency flip indistinguishable from default")
		}
		if got := r.Concurrency(); got != 1 {
			t.Errorf("Concurrency() = %d, want 1 (the value runFix feeds DriveOptions.Concurrency/Workers)", got)
		}
		if r.Concurrency() == config.DefaultConcurrency() {
			t.Errorf("Concurrency() did not change from the NumCPU default")
		}
	})

	t.Run("verify.max_changed_lines reaches diff-bounded", func(t *testing.T) {
		if got := r.MaxChangedLines(); got != 7 {
			t.Errorf("MaxChangedLines() = %d, want 7 (the value runFix feeds RecipeConfig.Limits)", got)
		}
		if r.MaxChangedLines() == config.DefaultMaxChangedLines {
			t.Errorf("MaxChangedLines() did not change from the built-in default")
		}
	})

	t.Run("verify.max_changed_files reaches diff-bounded", func(t *testing.T) {
		if got := r.MaxChangedFiles(); got != 3 {
			t.Errorf("MaxChangedFiles() = %d, want 3 (the value runFix feeds RecipeConfig.Limits)", got)
		}
		if r.MaxChangedFiles() == config.DefaultMaxChangedFiles {
			t.Errorf("MaxChangedFiles() did not change from the built-in default")
		}
	})
}
