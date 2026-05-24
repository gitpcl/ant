package main

import (
	"github.com/gitpcl/ant/internal/engine/apply"
	"github.com/gitpcl/ant/internal/engine/stage"
	store "github.com/gitpcl/ant/internal/engine/store"
	"github.com/spf13/cobra"
)

// newApplyCmd builds `ant apply [--no-branch]` (TECHSPEC §7). It lands the
// ACCEPTED (marked) staged diffs into the working tree, on a new branch by
// default; --no-branch lands on the current branch. Unaccepted diffs are not
// applied. This handler is a thin front door: it resolves the run + staging
// area, filters to the accepted set, and calls the engine's go-git apply (the
// boundary test keeps go-git out of cmd/ant).
func newApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply [run]",
		Short: "Land accepted staged diffs, on a branch by default",
		Long: "Apply lands the diffs accepted in `ant review` into the working tree as " +
			"commits, on a new branch by default. --no-branch lands on the current " +
			"branch. Diffs not accepted are left staged and untouched.",
		Args: cobra.MaximumNArgs(1),
		RunE: runApply,
	}
	cmd.Flags().Bool("no-branch", false, "apply onto the current branch instead of a new one")
	cmd.Flags().String("path", ".", "working-tree root (the git repository to land into)")
	return cmd
}

// runApply resolves the latest (or named) run, filters its staged records to the
// accepted set, and lands them via the engine apply path. With nothing accepted
// it reports cleanly and exits 0 (nothing to do is success, not an error).
func runApply(cmd *cobra.Command, args []string) error {
	path, _ := cmd.Flags().GetString("path")
	st := store.New(path)

	runID := ""
	if len(args) > 0 {
		runID = args[0]
	}
	if runID == "" {
		latest, err := st.LatestRunID()
		if err != nil {
			return err
		}
		runID = latest
	}

	jsonOut := boolFlag(cmd, "json")
	return apply.Drive(cmd.Context(), cmd.OutOrStdout(), stage.New(st, runID), apply.DriveOptions{
		Root:     path,
		RunID:    runID,
		NoBranch: boolFlag(cmd, "no-branch"),
		JSON:     jsonOut,
	})
}
