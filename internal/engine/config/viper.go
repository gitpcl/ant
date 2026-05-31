package config

import (
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Bind builds the single viper instance that owns config precedence for a run
// (TECHSPEC §9). It binds the command's flags (the highest band), reads ant.toml
// into the config-file band when present, and surfaces unknown-key warnings —
// all so cmd/ant never re-implements precedence. The defaults/manifest band is
// layered on later by NewResolver, which seeds SetDefault. The returned warnings
// are the unknown-key lines the CLI prints to stderr.
//
// flagToKey maps the CLI's flat flag names ("fixer") to the dotted config keys
// ("colony.fixer") so a flag and its [colony] counterpart compete on the same
// viper key. Only the colony-level knobs are bound here; per-species sections
// are read from the typed Config (SpeciesConfig), not through viper, because
// their keys are dynamic (one per species).
//
// A missing ant.toml is not an error — zero-config must work — so a not-found
// read is swallowed and warnings is nil. A malformed file is an operational
// error (exit 2) returned to the caller.
func Bind(flags *pflag.FlagSet, configPath string) (*viper.Viper, []string, error) {
	v := viper.New()

	flagToKey := map[string]string{
		"concurrency": KeyConcurrency,
		"fixer":       KeyFixer,
		"model":       KeyModel,
	}
	for flag, key := range flagToKey {
		if f := flags.Lookup(flag); f != nil {
			_ = v.BindPFlag(key, f)
		}
	}

	path := configPath
	if path == "" {
		path = DefaultConfigName
	}

	cfg, warnings, found, err := LoadStrict(path)
	if err != nil {
		return nil, nil, err // malformed config → operational (exit 2)
	}
	if !found {
		return v, nil, nil // zero-config: flags + defaults only
	}

	// Feed the recognized [colony] and [verify] values into viper's config-file
	// band (below flags, above defaults) so the precedence order holds with viper
	// as the one authority. Per-species and ignore values stay on the typed Config.
	merged := map[string]any{}

	colony := map[string]any{}
	if cfg.Colony.Concurrency != nil {
		colony["concurrency"] = *cfg.Colony.Concurrency
	}
	if cfg.Colony.Fixer != nil {
		colony["fixer"] = *cfg.Colony.Fixer
	}
	if cfg.Colony.Model != nil {
		colony["model"] = *cfg.Colony.Model
	}
	if len(colony) > 0 {
		merged["colony"] = colony
	}

	// [verify] has no bound flags (the size caps are config-only), so its sole
	// non-default source is ant.toml. Merging it into the config-file band is what
	// makes [verify].max_changed_lines/max_changed_files reach Resolver.MaxChanged*
	// (the values runFix feeds verify.Limits) — without this, the keys parse into
	// the typed Config but never populate the viper key the resolver reads, leaving
	// the knob inert (Sprint 022 Finding 2). A configured 0 is preserved (the
	// pointer distinguishes absent from zero), so "unbounded" is expressible.
	verifySec := map[string]any{}
	if cfg.Verify.MaxChangedLines != nil {
		verifySec["max_changed_lines"] = *cfg.Verify.MaxChangedLines
	}
	if cfg.Verify.MaxChangedFiles != nil {
		verifySec["max_changed_files"] = *cfg.Verify.MaxChangedFiles
	}
	if len(verifySec) > 0 {
		merged["verify"] = verifySec
	}

	if len(merged) > 0 {
		v.SetConfigType("toml")
		if err := v.MergeConfigMap(merged); err != nil {
			return nil, nil, err
		}
	}

	return v, warnings, nil
}
