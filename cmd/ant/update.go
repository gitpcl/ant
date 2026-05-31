package main

import (
	"fmt"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/selfupdate"
	"github.com/spf13/cobra"
)

// update.go is the thin front door for `ant update`. It parses flags, prints the
// current version, and hands off to internal/engine/selfupdate, which re-runs the
// official installer (checksum-verified, atomic install). No update logic lives
// here (TECHSPEC §3): the CLI only composes options and streams the installer's
// output.
func newUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update ant to the latest release (or a pinned version)",
		Long: "Update downloads the official installer and runs it to fetch and install " +
			"the latest release, replacing this binary in place. The installer verifies " +
			"the release's SHA-256 checksum before installing, so a corrupted or tampered " +
			"download aborts the update.\n\n" +
			"Pin a release with --version (e.g. --version v0.3.0) and choose the install " +
			"directory with --dir. Uses the POSIX installer (macOS/Linux); on Windows, " +
			"download the release archive from the releases page.",
		Args: cobra.NoArgs,
		RunE: runUpdate,
	}
	cmd.Flags().String("version", "", "release tag to install (e.g. v0.3.0); default: latest")
	cmd.Flags().String("dir", "", "bin directory to install into (default: a writable PATH dir)")
	return cmd
}

func runUpdate(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	version, _ := cmd.Flags().GetString("version")
	dir, _ := cmd.Flags().GetString("dir")

	target := version
	if target == "" {
		target = "latest"
	}
	fmt.Fprintf(out, "Current version: %s\n", engine.Version)
	fmt.Fprintf(out, "Installing:      %s\n\n", target)

	return selfupdate.Run(cmd.Context(), selfupdate.Options{
		Version:    version,
		InstallDir: dir,
	}, out)
}
