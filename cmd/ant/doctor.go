package main

import (
	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/doctor"
	"github.com/spf13/cobra"
)

// doctor.go is the thin front door for `ant doctor` (Sprint 022 missing
// feature). It parses flags, resolves the working-tree root + config + species
// paths, calls the engine's doctor.Run (which owns every check), renders the
// report in the format the --json flag selects, and maps "not ready" to a
// CI-appropriate non-zero exit. No check logic lives here (TECHSPEC §3): the CLI
// only composes inputs and renders the engine's Report.
func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor [path]",
		Short: "Check environment readiness (tools, model endpoint, git, ant.toml)",
		Long: "Doctor probes the environment ant runs in: required external tools " +
			"(ast-grep / goimports / ruff, derived from the enabled species' capability " +
			"metadata), the model-endpoint env vars, the git repository, and ant.toml " +
			"validity. It emits a human report by default and a single JSON document with " +
			"--json. It exits non-zero only when a REQUIRED capability is missing; advisory " +
			"warnings (e.g. an unset model endpoint, no git repo) keep the exit at zero.",
		Args: cobra.ArbitraryArgs,
		RunE: runDoctor,
	}
	cmd.Flags().String("path", ".", "working-tree root to probe (where .ant/species lives)")
	return cmd
}

// runDoctor composes the engine inputs, runs the probe, renders, and classifies
// the exit code. "Not ready" is an operational failure (exit 2) — the same class
// as a malformed config the run path would reject — carried by a typed error so
// the centralized exit handler maps it without inspecting engine internals.
func runDoctor(cmd *cobra.Command, args []string) error {
	path, _ := cmd.Flags().GetString("path")
	if len(args) > 0 && args[0] != "" {
		path = args[0]
	}

	report := doctor.Run(doctor.Options{
		Root:            path,
		ConfigPath:      configFileOrDefault(configPathFlag(cmd)),
		SpeciesUserRoot: userSpeciesRootFor(path),
	})

	format := doctor.FormatHuman
	if boolFlag(cmd, "json") {
		format = doctor.FormatJSON
	}
	if err := doctor.Render(cmd.OutOrStdout(), format, report); err != nil {
		return err
	}

	if !report.Ready {
		return &notReadyError{}
	}
	return nil
}

// notReadyError signals that a REQUIRED doctor check failed. It carries the
// operational exit code (2) so a CI step that runs `ant doctor` fails the build
// when the environment cannot run ant — distinct from the findings gate (exit 1).
// The report was already rendered, so Error() is only the single-line stderr
// diagnostic the root handler prints.
type notReadyError struct{}

func (e *notReadyError) Error() string {
	return "doctor: environment not ready (a required capability is missing)"
}

func (e *notReadyError) ExitCode() int { return engine.ExitOperational }
