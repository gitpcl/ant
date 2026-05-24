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

	// Feed the recognized [colony] values into viper's config-file band (below
	// flags, above defaults) so the precedence order holds with viper as the one
	// authority. Per-species and ignore values stay on the typed Config.
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
		v.SetConfigType("toml")
		if err := v.MergeConfigMap(map[string]any{"colony": colony}); err != nil {
			return nil, nil, err
		}
	}

	return v, warnings, nil
}
