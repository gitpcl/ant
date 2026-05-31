package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/gitpcl/ant/internal/engine/config"
	"github.com/gitpcl/ant/internal/engine/species"
	store "github.com/gitpcl/ant/internal/engine/store"
	"github.com/spf13/cobra"
)

// species.go holds the `ant species` subcommand handlers. They are thin front
// doors: each parses flags/args and calls into internal/engine/species, which
// owns the clone + validation + placement + removal logic and the trust
// authority. The boundary test (boundary_test.go) keeps go-git, os/exec, and
// persistence out of cmd/ant — the handlers here only resolve paths + the trust
// store seam and delegate, then render the result.
//
// install (security stage) clones + validates structure. list and remove
// (engineer stage) read the resolver + trust authority and the on-disk tree;
// neither duplicates resolution or trust logic.

// newSpeciesInstallCmd builds `ant species install <git-url>` (TECHSPEC §7). It
// clones the target repo and places well-formed species folders under
// .ant/species/, running NO repo code at install time (the engine enforces the
// no-exec property). Installed species are propose-only on first use via the
// freshly-installed trust override — this handler grants no trust.
func newSpeciesInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install <git-url>",
		Short: "Install a community species repo (validates structure, runs no repo code)",
		Long: "Install clones a git repository and places every well-formed species " +
			"folder (a directory with a parseable species.toml) under .ant/species/. " +
			"It runs NO code from the repository — setup scripts, verify scripts, and " +
			"generate directives never execute at install. An installed species is " +
			"propose-only on its first run until you review its output once.",
		Args: cobra.ExactArgs(1),
		RunE: runSpeciesInstall,
	}
	cmd.Flags().String("path", ".", "working-tree root (where .ant/species lives)")
	return cmd
}

// runSpeciesInstall resolves the destination .ant/species directory and calls
// the engine installer. It renders the installed set (or a tailored message when
// the repo holds no species) and lets the typed engine error classify the exit
// code (operational → 2).
func runSpeciesInstall(cmd *cobra.Command, args []string) error {
	url := args[0]
	path, _ := cmd.Flags().GetString("path")
	dest := userSpeciesRootFor(path)

	installed, err := species.Install(cmd.Context(), species.InstallOptions{
		URL:      url,
		DestRoot: dest,
	})
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	for _, in := range installed {
		fmt.Fprintf(out, "installed species %q into %s\n", in.Name, in.Path)
	}
	fmt.Fprintf(out, "%d species installed. They are propose-only until reviewed once (run `ant fix` then `ant review`).\n", len(installed))
	return nil
}

// newSpeciesListCmd builds `ant species list` (TECHSPEC §7). It enumerates every
// species — built-in (embedded) and user-installed (.ant/species/) — and renders
// each one's origin, enabled/disabled state, and EFFECTIVE trust level after the
// §6.3 freshly-installed override is folded in. It mutates nothing.
func newSpeciesListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Show available species and effective trust level",
		Long: "List enumerates every species (built-in and user-installed) with its " +
			"origin, enabled/disabled state, and effective trust level. A " +
			"freshly-installed species is shown as propose-only until reviewed once, " +
			"even if its manifest requests auto-apply (TECHSPEC §6.3).",
		Args: cobra.NoArgs,
		RunE: runSpeciesList,
	}
	cmd.Flags().String("path", ".", "working-tree root (where .ant/species lives)")
	return cmd
}

// runSpeciesList resolves the merged species set, folds the trust authority over
// it, and renders one row per species. Resolution (Resolver.Resolve) and the
// trust decision (ResolveTrust over the Store) are reused verbatim from the
// engine — the same authorities `ant fix` gates on — so the listed trust matches
// what the apply path would do. The handler only reads config + builds the store
// seam and renders.
func runSpeciesList(cmd *cobra.Command, args []string) error {
	path, _ := cmd.Flags().GetString("path")
	userRoot := userSpeciesRootFor(path)

	cfg, _, err := config.Load(configFileOrDefault(configPathFlag(cmd)))
	if err != nil {
		return err // malformed ant.toml → operational (exit 2)
	}

	resolved, err := species.NewResolver(userRoot, nil).Resolve(cfg)
	if err != nil {
		return err // a malformed species manifest → operational (exit 2)
	}

	decisions, err := species.ResolveTrust(resolved, store.New(path))
	if err != nil {
		return err // cannot read trust state → operational (exit 2)
	}

	renderSpeciesList(cmd.OutOrStdout(), decisions)
	return nil
}

// renderSpeciesList prints the resolved+trust-decided species as an aligned
// table: NAME, ORIGIN (built-in|installed), STATE (enabled|disabled), and TRUST.
// The trust column shows the EFFECTIVE decision: "auto-apply" when a verified
// diff may auto-land, otherwise "propose-only". A species held back solely by
// the §6.3 freshly-installed override is marked distinctly so the user sees it is
// gated until reviewed, not configured propose-only.
func renderSpeciesList(out io.Writer, decisions []species.TrustDecision) {
	if len(decisions) == 0 {
		fmt.Fprintln(out, "no species available")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tORIGIN\tSTATE\tTRUST")
	for _, d := range decisions {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			d.Resolved.Manifest.Name,
			originLabel(d.Resolved.Origin),
			stateLabel(d.Resolved.EffectiveEnabled),
			trustLabel(d),
		)
	}
	tw.Flush()
}

// originLabel renders provenance in user terms: an embedded built-in vs a
// user-installed species under .ant/species/.
func originLabel(o species.Origin) string {
	if o == species.OriginUser {
		return "installed"
	}
	return "built-in"
}

// stateLabel renders the effective enabled flag.
func stateLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

// trustLabel renders the EFFECTIVE trust decision. A freshly-installed species
// (configured auto-apply but held back by the §6.3 override) is marked distinctly
// so it is clear the gating is temporary — until its output is reviewed once —
// rather than a configured propose-only setting.
func trustLabel(d species.TrustDecision) string {
	if d.FreshlyInstalled {
		return "propose-only (new — until reviewed)"
	}
	if d.EffectiveAutoApply {
		return "auto-apply"
	}
	return "propose-only"
}

// newSpeciesRemoveCmd builds `ant species remove <name>` (TECHSPEC §7). It
// deletes an installed community species and clears its trust state, refusing to
// remove embedded built-ins. The delete + trust clear live in the engine; this
// handler resolves the user root + trust store seam and delegates.
func newSpeciesRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an installed community species",
		Long: "Remove deletes the named species from .ant/species/ and clears its " +
			"trust state, so a later reinstall is treated as freshly installed again " +
			"(propose-only until reviewed). Built-in species ship in the binary and " +
			"cannot be removed.",
		Args: cobra.ExactArgs(1),
		RunE: runSpeciesRemove,
	}
	cmd.Flags().String("path", ".", "working-tree root (where .ant/species lives)")
	return cmd
}

// runSpeciesRemove resolves the user species root + trust store and calls the
// engine remover. The typed engine error classifies the exit code (built-in,
// missing, or unsafe name → operational, exit 2). On success it prints a single
// confirmation line.
func runSpeciesRemove(cmd *cobra.Command, args []string) error {
	name := args[0]
	path, _ := cmd.Flags().GetString("path")
	userRoot := userSpeciesRootFor(path)

	if err := species.Remove(species.RemoveOptions{
		Name:     name,
		UserRoot: userRoot,
		Trust:    store.New(path),
	}); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "removed species %q (trust state cleared)\n", name)
	return nil
}
