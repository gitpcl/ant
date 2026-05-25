package main

import (
	"sort"

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

	// Collect the species whose output is being reviewed BEFORE the walk, so a
	// completed review pass can lift the freshly-installed propose-only override
	// (TECHSPEC §6.3) for exactly those species. A review pass over a species'
	// output is the one human check that earns it its configured trust.
	reviewed := reviewedSpecies(area)

	if err := review.Run(cmd.Context(), cmd.OutOrStdout(), area, opts); err != nil {
		return err
	}

	// One review pass completed: record each reviewed species so its CONFIGURED
	// trust applies on subsequent runs. A marking failure is non-fatal (the
	// override simply stays conservative — the safe direction).
	if len(reviewed) > 0 {
		_ = st.MarkReviewed(reviewed...)
	}
	return nil
}

// reviewedSpecies returns the distinct species names that own the staged records
// in area, in sorted order. It is the set whose output a completed `ant review`
// pass walked — the species that have earned their configured trust under the
// freshly-installed override. A load error yields no names (the override stays
// conservative rather than guessing).
func reviewedSpecies(area *stage.Area) []string {
	records, err := area.ListRecords()
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	for _, rec := range records {
		if rec.Finding.Species != "" {
			seen[rec.Finding.Species] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
