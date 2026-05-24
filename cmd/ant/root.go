package main

import (
	"fmt"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/spf13/cobra"
)

// exitCoder is implemented by errors that carry their own process exit code, so
// the CLI can map an engine error to the right status (TECHSPEC §7.1) without
// importing the engine's error internals beyond engine.ExitCode.
type exitCoder interface{ ExitCode() int }

// findingsGateError signals that scout found something at or above the
// --fail-on threshold (exit code 1). It is not an operational failure — it is
// the CI gate firing — so it carries its own exit code distinct from the
// engine's operational (exit 2) classification.
type findingsGateError struct{ highest engine.Severity }

func (e *findingsGateError) Error() string {
	return fmt.Sprintf("findings at or above the --fail-on threshold (highest: %s)", e.highest)
}

func (e *findingsGateError) ExitCode() int { return engine.ExitFindings }

// executeWithExitCode runs the command tree and converts any error to the
// process exit code. A nil error is success (0); a findingsGateError is the CI
// gate (1); everything else is classified by the engine (operational → 2).
//
// cobra's own error printing is silenced (root SilenceErrors) so the engine's
// event-stream renderer owns user-facing output; this boundary prints a single
// diagnostic line to stderr for failures the engine did not surface inline
// (e.g. flag-validation errors that abort before any run). The findings gate
// (exit 1) is not an error condition to report — scout already rendered the
// findings — so it prints nothing.
func executeWithExitCode(root *cobra.Command) int {
	err := root.Execute()
	if err == nil {
		return engine.ExitOK
	}
	if coder, ok := err.(exitCoder); ok {
		return coder.ExitCode()
	}
	fmt.Fprintln(root.ErrOrStderr(), "ant:", err)
	return engine.ExitCode(err)
}

// newRootCmd builds the cobra command tree for the frozen v1 command surface
// (ADR 0001 / TECHSPEC §7). Bare `ant` aliases to scout (ADR 0001). The leaf
// commands not yet implemented print a clean "not yet implemented" line and
// return without error; scout is fully wired this sprint.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "ant [path]",
		Short:        "Ant — an autonomous code-cleanup colony",
		Long:         "Ant detects, fixes, and verifies code-cleanup tasks. Bare `ant` runs scout.",
		Version:      engine.Version,
		SilenceUsage: true,
		// cobra must not print the error itself: it would pollute the
		// machine-readable --json stream on stdout. Failures surface through the
		// engine's event stream (the --json run.end error field) and a single
		// stderr diagnostic from executeWithExitCode; the exit code propagates
		// there too.
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		// Bare `ant` is an alias for `ant scout` with summary output (ADR 0001).
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScout(cmd, args)
		},
	}

	// Global flags from TECHSPEC §7. Parsing lives here; resolution lives in the
	// engine's config layer (a later sprint wires viper precedence fully).
	pf := root.PersistentFlags()
	pf.Bool("json", false, "emit the structured event stream")
	pf.String("fail-on", "", "CI exit threshold: low|medium|high")
	pf.String("config", "", "path to ant.toml")
	pf.String("fixer", "", "fixer adapter to use")
	pf.String("model", "", "model id for the fixer")
	pf.Int("concurrency", 0, "max parallel ants (default: NumCPU)")

	root.AddCommand(
		newScoutCmd(),
		leaf("fix [path]", "Produce verified staged diffs (apply only with --apply)"),
		leaf("review", "Walk staged diffs: accept/skip/diff/explain/next/quit"),
		leaf("apply", "Land accepted staged diffs, on a branch by default"),
		leaf("init", "Scaffold an ant.toml config file"),
		speciesCmd(),
	)
	return root
}

// leaf builds a stub subcommand whose behavior lands in a later sprint. The
// first word of use is the command name.
func leaf(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE:  notYetImplemented(firstWord(use)),
	}
}

// speciesCmd builds the `ant species` parent with its list/install/remove
// children (TECHSPEC §7).
func speciesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "species",
		Short: "Manage detection species",
	}
	cmd.AddCommand(
		leaf("list", "Show available species and effective trust level"),
		leaf("install <git-url>", "Install a community species repo"),
		leaf("remove <name>", "Remove an installed community species"),
	)
	return cmd
}

// notYetImplemented returns a RunE that reports the command is a skeleton stub
// and returns cleanly (exit 0). It is pure rendering — no engine logic.
func notYetImplemented(name string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		fmt.Fprintf(cmd.OutOrStdout(), "ant %s: not yet implemented (engine v%s)\n", name, engine.Version)
		return nil
	}
}

// firstWord returns the leading token of a cobra Use string.
func firstWord(use string) string {
	for i := 0; i < len(use); i++ {
		if use[i] == ' ' {
			return use[:i]
		}
	}
	return use
}
