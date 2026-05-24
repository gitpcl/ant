// Command ant is the thin CLI front door for the Ant colony engine. It only
// parses flags, calls into internal/engine, and renders results — all logic
// lives in the engine (TECHSPEC §3 hard rule). The enterprise layer imports the
// engine as a library; this CLI is one of several front doors over it.
package main

import (
	"fmt"
	"os"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// cobra already printed the error; exit non-zero for scripts/CI.
		os.Exit(1)
	}
}

// newRootCmd builds the cobra command tree for the frozen v1 command surface
// (ADR 0001 / TECHSPEC §7). The leaf commands are M1 skeleton stubs; their full
// behavior lands in later sprints and is implemented in the engine, not here.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "ant [path]",
		Short:         "Ant — an autonomous code-cleanup colony",
		Long:          "Ant detects, fixes, and verifies code-cleanup tasks. Bare `ant` runs scout.",
		Version:       engine.Version,
		SilenceUsage:  true,
		SilenceErrors: false,
		// Bare `ant` aliases to scout (ADR 0001). Wiring lands in the cli sprint.
		RunE: notYetImplemented("scout"),
	}

	// Global flags from TECHSPEC §7. Parsing lives here; resolution lives in the
	// engine's config layer (later sprint).
	root.PersistentFlags().Bool("json", false, "emit the structured event stream")
	root.PersistentFlags().String("fail-on", "", "CI exit threshold: low|medium|high")
	root.PersistentFlags().String("config", "", "path to ant.toml")
	root.PersistentFlags().String("fixer", "", "fixer adapter to use")
	root.PersistentFlags().String("model", "", "model id for the fixer")

	root.AddCommand(
		leaf("scout [path]", "Run detectors and report findings (mutates nothing)"),
		leaf("fix [path]", "Produce verified staged diffs (apply only with --apply)"),
		leaf("review", "Walk staged diffs: accept/skip/diff/explain/next/quit"),
		leaf("apply", "Land accepted staged diffs, on a branch by default"),
		leaf("init", "Scaffold an ant.toml config file"),
		speciesCmd(),
	)
	return root
}

// leaf builds a stub subcommand. The first word of use is the command name.
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
// and returns cleanly. It is pure rendering — no engine logic.
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
