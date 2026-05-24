package main

import (
	"github.com/gitpcl/ant/internal/engine/review"
	"github.com/gitpcl/ant/internal/engine/stage"
	store "github.com/gitpcl/ant/internal/engine/store"
	"github.com/spf13/cobra"
)

// newReviewCmd builds `ant review [run]` (TECHSPEC §7). It walks the staged
// diffs `ant fix` left, one at a time, marking each accept/skip; it mutates
// nothing on disk. This handler is a thin front door: it resolves the run id and
// the staging area, then calls review.Run — the engine owns the Bubble Tea
// program (goroutines), the six verbs, and the mark persistence.
func newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review [run]",
		Short: "Walk staged diffs: accept/skip/diff/explain/next/quit",
		Long: "Review walks the diffs `ant fix` staged, showing the patch and its " +
			"provenance, and marks each accepted or skipped. Accepted diffs are landed " +
			"by `ant apply`. Review never writes the working tree.",
		Args: cobra.MaximumNArgs(1),
		RunE: runReview,
	}
	cmd.Flags().String("path", ".", "working-tree root holding the staged state")
	cmd.Flags().Bool("ascii", false, "use ASCII glyphs instead of Unicode")
	return cmd
}

// runReview resolves the latest (or named) run's staged area and launches the
// review TUI. With no run id it picks the most recent run from the Store.
func runReview(cmd *cobra.Command, args []string) error {
	path, _ := cmd.Flags().GetString("path")
	st := store.New(path)

	runID := ""
	if len(args) > 0 {
		runID = args[0]
	}
	if runID == "" {
		// No run named: review the most recent one `ant fix` produced. An empty
		// id (no runs recorded yet) flows through to review.Run, which renders the
		// empty-state screen (review-interaction.md §5.1) rather than erroring.
		latest, err := st.LatestRunID()
		if err != nil {
			return err
		}
		runID = latest
	}

	area := stage.New(st, runID)
	opts := review.Options{
		RunID: runID,
		Ascii: asciiEnabled(cmd),
		Color: colorEnabled(),
	}
	return review.Run(cmd.Context(), cmd.OutOrStdout(), area, opts)
}
