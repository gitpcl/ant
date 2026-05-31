package main

import (
	"github.com/gitpcl/ant/internal/engine/explain"
	store "github.com/gitpcl/ant/internal/engine/store"
	"github.com/spf13/cobra"
)

// explain.go is the thin front door for `ant explain <run>|<finding>` (Sprint
// 022 missing feature). It resolves the working-tree root, opens the local
// Store, hands the reference to the engine's explain.Resolve (which loads the
// run/finding), and renders in the format --json selects. No load/resolve/render
// logic lives here (TECHSPEC §3): the CLI only composes inputs and renders the
// engine's Detail, and maps a not-found/bad-reference error to the centralized
// exit handler (operational → exit 2 via engine.ErrOperational).
func newExplainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "explain <run>|<run>#<finding-index>",
		Short: "Show detail for a run or a single finding (human or --json)",
		Long: "Explain loads a persisted run, or a single finding within a run, from the " +
			"local .ant/state store and prints its detail. Address a run by its ID " +
			"(as shown by `ant scout`/`ant fix` and in --json run.start events); address a " +
			"finding by `<runID>#<index>`, a 0-based index into that run's findings. It " +
			"emits a human report by default and a single JSON document with --json, for " +
			"CI and front-door integrations. A missing run or bad reference exits non-zero.",
		Args: cobra.ExactArgs(1),
		RunE: runExplain,
	}
	cmd.Flags().String("path", ".", "working-tree root (where .ant/state lives)")
	return cmd
}

// runExplain composes the Store, resolves the reference through the engine, and
// renders. A resolution error (missing run, malformed finding reference) is
// returned verbatim — it already wraps engine.ErrOperational, so the root
// handler maps it to exit code 2 without inspecting engine internals.
func runExplain(cmd *cobra.Command, args []string) error {
	path, _ := cmd.Flags().GetString("path")
	st := store.New(path)

	detail, err := explain.Resolve(st, args[0])
	if err != nil {
		return err
	}

	format := explain.FormatHuman
	if boolFlag(cmd, "json") {
		format = explain.FormatJSON
	}
	return explain.Render(cmd.OutOrStdout(), format, detail)
}
