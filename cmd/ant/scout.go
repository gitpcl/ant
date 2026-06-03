package main

import (
	"fmt"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/colony"
	"github.com/gitpcl/ant/internal/engine/config"
	"github.com/gitpcl/ant/internal/engine/scout"
	"github.com/gitpcl/ant/internal/engine/species"
	store "github.com/gitpcl/ant/internal/engine/store"
	"github.com/spf13/cobra"
)

// newScoutCmd builds `ant scout [path] [--ant ...] [--severity ...] [--detail]`
// (TECHSPEC §7). scout runs detectors and reports findings; it mutates nothing.
func newScoutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scout [path]",
		Short: "Run detectors and report findings (mutates nothing)",
		Long: "Scout runs the enabled species' detectors over the scope and reports " +
			"findings. It never writes to the working tree.\n\n" +
			"By default the human output is a severity-led DIGEST: every high finding " +
			"in full, then medium/low folded to per-species counts. Use --all to list " +
			"every finding one per line (the full flat list), and --detail to add the " +
			"code snippet to each line.\n\n" +
			"Noise directories (vendor, node_modules, .git, and nested testdata) are " +
			"ignored by default; [ignore].paths in ant.toml extends that set, and " +
			"--no-default-ignore drops the built-in defaults.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScout(cmd, args)
		},
	}
	cmd.Flags().StringSlice("ant", nil, "limit the run to the named species (repeatable)")
	cmd.Flags().String("severity", "", "only report findings at or above: low|medium|high")
	cmd.Flags().Bool("detail", false, "verbose per-finding output (adds the code snippet)")
	cmd.Flags().Bool("all", false, "list every finding (the full flat list) instead of the severity digest")
	cmd.Flags().Bool("no-default-ignore", false, "do not skip the built-in noise dirs (vendor, node_modules, .git, testdata)")
	return cmd
}

// runScout is the shared handler for `ant scout` and bare `ant` (the alias from
// ADR 0001). It parses flags, builds the scope and the built-in detector set,
// drives the engine's scout run (which owns the bus, renderer, and
// concurrency), and applies the --fail-on CI gate. No business logic lives here
// — composition and rendering selection only.
func runScout(cmd *cobra.Command, args []string) error {
	// Load ant.toml through the engine's config layer so unknown keys surface as
	// warnings (TECHSPEC §9) and precedence stays owned by one authority. A
	// malformed config fails fast (exit 2) and a typo is reported on every run,
	// not silently ignored. The parsed config also feeds species resolution below
	// so scout sees the SAME enabled/disabled set `ant fix` does (Sprint 022).
	cfg, err := surfaceConfigWarnings(cmd)
	if err != nil {
		return err // malformed ant.toml → operational (exit 2)
	}

	// Build the SAME config.Resolver `ant fix` uses so the effective [ignore].paths
	// (and any future colony knob scout needs) comes from one precedence authority
	// — scout and fix can never drift on what they ignore. config.Bind reads
	// ant.toml into viper's file band; NewResolver seeds the defaults/manifest band.
	// Scout exposes no ignore-affecting flags, so IgnorePaths() here equals the
	// resolver value reaching the fix front door.
	v, _, err := config.Bind(cmd.Flags(), configPathFlag(cmd))
	if err != nil {
		return err // malformed ant.toml → operational (exit 2)
	}
	resolver := config.NewResolver(v, config.ManifestDefaults{})

	path := "."
	if len(args) > 0 && args[0] != "" {
		path = args[0]
	}

	format := scout.FormatHuman
	if boolFlag(cmd, "json") {
		format = scout.FormatJSON
	}

	// Validate severity-valued flags up front so bad input fails fast (exit 2)
	// before any work runs — never trust input past the boundary.
	severityFilter, err := parseOptionalSeverity(cmd, "severity")
	if err != nil {
		return err
	}
	failOn, err := parseOptionalSeverity(cmd, "fail-on")
	if err != nil {
		return err
	}

	// Effective ignore globs = built-in noise-dir defaults (vendor, node_modules,
	// .git, nested testdata) PLUS the user's [ignore].paths, unless
	// --no-default-ignore drops the defaults. The defaults are segment-anchored
	// so scanning INTO a noise-named dir still reports findings (engine.PathIgnored).
	scope := engine.Scope{
		Root:        path,
		Species:     stringSlice(cmd, "ant"),
		IgnoreGlobs: effectiveIgnoreGlobs(resolver.IgnorePaths(), boolFlag(cmd, "no-default-ignore")),
	}

	// Resolve the full species set through the SAME path `ant fix` uses
	// (species.NewResolver(...).Resolve over the loaded config), so scout reports
	// every built-in + installed + config-enabled species and honors enabled/
	// disabled identically — no more hard-coded 2-species table (Sprint 022
	// Finding 1). User species resolve under the target path (see
	// userSpeciesRootFor). The CLI does no merge/precedence logic; the resolver owns it.
	resolved, err := species.NewResolver(userSpeciesRootFor(path), nil).Resolve(cfg)
	if err != nil {
		return err // a malformed species manifest → operational (exit 2)
	}

	// Resolve the per-species TRUST decision against the target's Store, the SAME
	// authority `ant fix` uses (species.ResolveTrust). Scout needs it so a vetted
	// built-in or reviewed installed command-detector species runs its real
	// detector, while an untrusted user species stays blocked-until-reviewed —
	// the trust gate now lives on the read-only path too, not only on fix.
	decisions, err := species.ResolveTrust(resolved, store.New(path))
	if err != nil {
		return err // operational (exit 2): cannot read trust state
	}

	// Materialize the embedded built-in rule files to disk so the ast-grep
	// detector (a shell-out plugin boundary, TECHSPEC §2) can read them. The
	// engine owns the extraction (species.MaterializeBuiltinRules over the
	// go:embed tree); the CLI just points the detector set at the resulting root
	// and cleans it up when the run ends.
	rulesRoot, cleanupRules, err := species.MaterializeBuiltinRules()
	if err != nil {
		return err // operational (exit 2): cannot stage built-in rules
	}
	defer cleanupRules()

	// Build scout's detector set from the resolved trust decisions via the colony
	// composition root — the SAME species→detector mapping `ant fix` uses. Trusted
	// command-detector species (vetted built-ins, reviewed installs) run their real
	// detector; untrusted ones surface as a scan-safe blocked-until-reviewed
	// detector (never silently dropped, never an unvetted scan-time script exec).
	opts := scout.Options{
		Scope:          scope,
		Detectors:      colony.ScoutDetectors(decisions, rulesRoot),
		SeverityFilter: severityFilter,
		AntFilter:      scope.Species,
	}

	render := scout.RenderOptions{Detail: boolFlag(cmd, "detail"), All: boolFlag(cmd, "all")}
	result, err := scout.Drive(cmd.Context(), cmd.OutOrStdout(), format, render, opts)
	if err != nil {
		return err // operational error → engine.ExitCode classifies it (exit 2)
	}

	return applyFailOn(failOn, result)
}

// effectiveIgnoreGlobs merges the built-in default-ignore globs with the user's
// configured [ignore].paths. The defaults come first (so user globs extend, not
// replace, them) and are skipped entirely when --no-default-ignore is set. The
// result is a fresh slice — neither input is mutated (coding-style: immutability).
func effectiveIgnoreGlobs(userGlobs []string, noDefaults bool) []string {
	if noDefaults {
		out := make([]string, len(userGlobs))
		copy(out, userGlobs)
		return out
	}
	out := make([]string, 0, len(config.DefaultIgnoreGlobs)+len(userGlobs))
	out = append(out, config.DefaultIgnoreGlobs...)
	out = append(out, userGlobs...)
	return out
}

// applyFailOn implements the --fail-on CI gate (TECHSPEC §7.1): if the highest
// finding severity meets or exceeds the threshold, return a findingsGateError
// (exit code 1). No threshold, or nothing over it, is success (exit 0). It
// never modifies anything — scout is read-only.
func applyFailOn(threshold engine.Severity, result scout.Result) error {
	if threshold == engine.SeverityUnknown {
		return nil // no gate configured
	}
	if result.HighestSeverity.AtLeast(threshold) {
		return &findingsGateError{highest: result.HighestSeverity}
	}
	return nil
}

// parseOptionalSeverity reads a severity-valued flag directly from cobra. An
// empty value means "not set" (SeverityUnknown). A non-empty invalid value is an
// operational error (bad input → exit 2) wrapped so it classifies correctly.
// These flags (--severity, --fail-on) are CLI-only — they never appear in
// ant.toml (TECHSPEC §9) — so they are read straight from the flag, not through
// the config precedence chain.
func parseOptionalSeverity(cmd *cobra.Command, flag string) (engine.Severity, error) {
	raw, err := cmd.Flags().GetString(flag)
	if err != nil || raw == "" {
		return engine.SeverityUnknown, nil
	}
	sev, perr := engine.ParseSeverity(raw)
	if perr != nil {
		return engine.SeverityUnknown, fmt.Errorf("%w: --%s: %v", engine.ErrOperational, flag, perr)
	}
	return sev, nil
}

// stringSlice reads a repeatable string flag, returning nil when unset.
func stringSlice(cmd *cobra.Command, flag string) []string {
	vals, err := cmd.Flags().GetStringSlice(flag)
	if err != nil {
		return nil
	}
	return vals
}

// boolFlag reads a boolean flag, returning false when unset or unreadable.
func boolFlag(cmd *cobra.Command, flag string) bool {
	val, err := cmd.Flags().GetBool(flag)
	if err != nil {
		return false
	}
	return val
}

// surfaceConfigWarnings loads ant.toml through the engine's config layer,
// prints any unknown-key warnings to stderr (TECHSPEC §9: unknown keys are
// warned, never silently ignored), and returns the parsed config so the caller
// can resolve species from it. It returns an operational error (exit 2) for a
// malformed file. The CLI does no precedence logic itself — the engine's config
// package owns the loader and the species resolver owns the merge; this only
// relays warnings and the typed error to the centralized exit-code handler.
func surfaceConfigWarnings(cmd *cobra.Command) (config.Config, error) {
	configPath := configPathFlag(cmd)
	cfg, warnings, _, err := config.LoadStrict(configFileOrDefault(configPath))
	if err != nil {
		return config.Config{}, err
	}
	for _, w := range warnings {
		fmt.Fprintln(cmd.ErrOrStderr(), "ant: warning:", w)
	}
	return cfg, nil
}

// configFileOrDefault returns the explicit --config path, or the conventional
// ant.toml in the working directory when none was given.
func configFileOrDefault(path string) string {
	if path != "" {
		return path
	}
	return config.DefaultConfigName
}
