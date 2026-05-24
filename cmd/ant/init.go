package main

import (
	"fmt"

	"github.com/gitpcl/ant/internal/engine/config"
	"github.com/spf13/cobra"
)

// newInitCmd builds `ant init` (TECHSPEC §7): scaffold a commented, parseable
// ant.toml. It is a thin front door — the scaffold content and the
// refuse-to-overwrite policy live in internal/engine/config (Scaffold); this
// handler only reads flags, calls the engine, renders the result, and lets the
// typed error classify the exit code (a refusal without --force is operational,
// exit 2; the file is never clobbered).
func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold an ant.toml config file",
		Long: "Init writes a commented ant.toml with sensible defaults. It refuses to " +
			"overwrite an existing file unless --force is given.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			force, _ := cmd.Flags().GetBool("force")
			path := configPathFlag(cmd)
			written, err := config.Scaffold(path, force)
			if err != nil {
				return err // ErrConfigExists / write failure → operational (exit 2)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", written)
			return nil
		},
	}
	cmd.Flags().Bool("force", false, "overwrite an existing ant.toml")
	return cmd
}

// configPathFlag returns the --config path if the user gave one, else "" so the
// engine uses its default filename (ant.toml in the working directory).
func configPathFlag(cmd *cobra.Command) string {
	if f := cmd.Flags().Lookup("config"); f != nil {
		return f.Value.String()
	}
	return ""
}
