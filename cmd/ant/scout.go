package main

import (
	"fmt"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/detect"
	"github.com/gitpcl/ant/internal/engine/scout"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
	v := configFor(cmd)

	path := "."
	if len(args) > 0 && args[0] != "" {
		path = args[0]
	}

	format := scout.FormatHuman
	if v.GetBool("json") {
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

	opts := scout.Options{
		Scope:          scope,
		Detectors:      detect.Builtins(builtinRulesRoot),
		SeverityFilter: severityFilter,
		AntFilter:      scope.Species,
	}

	result, err := scout.Drive(cmd.Context(), cmd.OutOrStdout(), format, v.GetBool("detail"), opts)
	if err != nil {
		return err // operational error → engine.ExitCode classifies it (exit 2)
	}

	return applyFailOn(failOn, result)
}

// builtinRulesRoot is where the built-in species rule files resolve from. It is
// empty in this sprint (the embedded species tree lands in a later sprint); the
// detector receives the bare relative rule path, which is sufficient for the
// recorded-fixture tests and surfaces a clear ast-grep error otherwise.
const builtinRulesRoot = ""

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

// parseOptionalSeverity reads a severity-valued flag. An empty value means "not
// set" (SeverityUnknown). A non-empty invalid value is an operational error
// (bad input → exit 2) wrapped so it classifies correctly.
func parseOptionalSeverity(cmd *cobra.Command, flag string) (engine.Severity, error) {
	raw := configFor(cmd).GetString(flag)
	if raw == "" {
		return engine.SeverityUnknown, nil
	}
	sev, err := engine.ParseSeverity(raw)
	if err != nil {
		return engine.SeverityUnknown, fmt.Errorf("%w: --%s: %v", engine.ErrOperational, flag, err)
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

// configFor builds a viper instance layering flags over ant.toml over defaults
// (TECHSPEC §9 resolution order). Flags bound here take precedence; ant.toml is
// read when present (zero-config works because a missing file is not an error).
// A fuller resolution chain (species manifest layer) lands in the config sprint;
// this wiring establishes the flags > ant.toml > defaults precedence the
// command surface depends on now.
func configFor(cmd *cobra.Command) *viper.Viper {
	v := viper.New()
	// Bind every flag the command exposes (local + inherited persistent) so
	// flag values win over file/default values.
	_ = v.BindPFlags(cmd.Flags())

	configPath := ""
	if f := cmd.Flags().Lookup("config"); f != nil {
		configPath = f.Value.String()
	}
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("ant")
		v.SetConfigType("toml")
		v.AddConfigPath(".")
	}
	// A missing config file is fine — bare `ant` must work zero-config.
	_ = v.ReadInConfig()
	return v
}
