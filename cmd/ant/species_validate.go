package main

import (
	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/species"
	"github.com/spf13/cobra"
)

// species_validate.go is the thin front door for `ant species validate [path]`
// (Sprint 022 missing feature, species authoring). It parses the optional folder
// argument, calls the engine's species.Validate (which owns the schema +
// referenced-file + capability checks and aggregates EVERY problem), renders in
// the format --json selects, and maps an invalid folder to a CI-appropriate
// non-zero exit. No validation logic lives here (TECHSPEC §3): the handler only
// resolves the path, calls Validate, renders the Report, and classifies the exit.
func newSpeciesValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate [path]",
		Short: "Validate a local species folder before publishing or installing",
		Long: "Validate checks a local species folder the way install/run would, " +
			"WITHOUT executing any of its code: it parses species.toml (strict — an " +
			"unknown key is an error), confirms every referenced file (detect rule, " +
			"command script, fix prompt, command: verifier) exists, and reports the " +
			"inferred capability metadata (report-only, requires-exec/network/tool). " +
			"It reports ALL problems at once so an author can fix the folder in one " +
			"pass. With no argument it validates the current directory. It emits a " +
			"human report by default and a single JSON document with --json, and exits " +
			"non-zero when the folder is not a well-formed species.",
		Args: cobra.MaximumNArgs(1),
		RunE: runSpeciesValidate,
	}
	return cmd
}

// runSpeciesValidate resolves the target folder (the positional arg or "." when
// omitted), validates it through the engine, renders, and classifies the exit.
// An invalid species is an operational failure (exit 2) — the same class as a
// malformed config the run path rejects — carried by a typed error so the
// centralized exit handler maps it without inspecting engine internals. The
// report is rendered first so the human/JSON output is present regardless of the
// exit code, exactly as `ant doctor` does for a not-ready environment.
func runSpeciesValidate(cmd *cobra.Command, args []string) error {
	dir := "."
	if len(args) > 0 && args[0] != "" {
		dir = args[0]
	}

	report := species.Validate(dir, nil)

	format := species.ValidationFormatHuman
	if boolFlag(cmd, "json") {
		format = species.ValidationFormatJSON
	}
	if err := species.RenderValidation(cmd.OutOrStdout(), format, report); err != nil {
		return err
	}

	if !report.OK {
		return &invalidSpeciesError{}
	}
	return nil
}

// invalidSpeciesError signals that the validated folder is not a well-formed
// species. It carries the operational exit code (2) so a CI step that runs
// `ant species validate` fails the build on a broken manifest — the same class
// as a malformed config. The report was already rendered, so Error() is only the
// single-line stderr diagnostic the root handler prints.
type invalidSpeciesError struct{}

func (e *invalidSpeciesError) Error() string {
	return "species validate: folder is not a well-formed species"
}

func (e *invalidSpeciesError) ExitCode() int { return engine.ExitOperational }
