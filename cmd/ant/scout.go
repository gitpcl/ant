package main

import (
	"fmt"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/config"
	"github.com/gitpcl/ant/internal/engine/detect"
	"github.com/gitpcl/ant/internal/engine/scout"
	"github.com/gitpcl/ant/internal/engine/species"
	"github.com/spf13/cobra"
)

// newScoutCmd builds `ant scout [path] [--ant ...] [--severity ...] [--detail]`
// (TECHSPEC §7). scout runs detectors and reports findings; it mutates nothing.
func newScoutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scout [path]",
		Short: "Run detectors and report findings (mutates nothing)",
		Long: "Scout runs the enabled species' detectors over the scope and reports " +
			"findings. It never writes to the working tree.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScout(cmd, args)
		},
	}
	cmd.Flags().StringSlice("ant", nil, "limit the run to the named species (repeatable)")
	cmd.Flags().String("severity", "", "only report findings at or above: low|medium|high")
	cmd.Flags().Bool("detail", false, "verbose per-finding output")
	return cmd
}

// runScout is the shared handler for `ant scout` and bare `ant` (the alias from
// ADR 0001). It parses flags, builds the scope and the built-in detector set,
// drives the engine's scout run (which owns the bus, renderer, and
// concurrency), and applies the --fail-on CI gate. No business logic lives here
// — composition and rendering selection only.
func runScout(cmd *cobra.Command, args []string) error {
	// Load ant.toml through the engine's config layer so unknown keys surface as
	// warnings (TECHSPEC §9) and precedence stays owned by one authority. scout
	// itself consumes no [colony] knob yet (fixer/model/concurrency feed `ant
	// fix`, a later sprint), but loading here means a malformed config fails fast
	// (exit 2) and a typo is reported on every run, not silently ignored.
	if err := surfaceConfigWarnings(cmd); err != nil {
		return err // malformed ant.toml → operational (exit 2)
	}

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

	scope := engine.Scope{
		Root:    path,
		Species: stringSlice(cmd, "ant"),
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

	opts := scout.Options{
		Scope:          scope,
		Detectors:      detect.Builtins(rulesRoot),
		SeverityFilter: severityFilter,
		AntFilter:      scope.Species,
	}

	result, err := scout.Drive(cmd.Context(), cmd.OutOrStdout(), format, boolFlag(cmd, "detail"), opts)
	if err != nil {
		return err // operational error → engine.ExitCode classifies it (exit 2)
	}

	return applyFailOn(failOn, result)
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

// surfaceConfigWarnings loads ant.toml through the engine's config layer and
// prints any unknown-key warnings to stderr (TECHSPEC §9: unknown keys are
// warned, never silently ignored). It returns an operational error (exit 2) for
// a malformed file. The CLI does no precedence logic itself — the engine's
// config package owns the loader and (for `ant fix`, later) the resolver; this
// only relays warnings and the typed error to the centralized exit-code handler.
func surfaceConfigWarnings(cmd *cobra.Command) error {
	configPath := configPathFlag(cmd)
	_, warnings, _, err := config.LoadStrict(configFileOrDefault(configPath))
	if err != nil {
		return err
	}
	for _, w := range warnings {
		fmt.Fprintln(cmd.ErrOrStderr(), "ant: warning:", w)
	}
	return nil
}

// configFileOrDefault returns the explicit --config path, or the conventional
// ant.toml in the working directory when none was given.
func configFileOrDefault(path string) string {
	if path != "" {
		return path
	}
	return config.DefaultConfigName
}
