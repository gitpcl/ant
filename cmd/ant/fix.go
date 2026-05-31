package main

import (
	"os"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/apply"
	"github.com/gitpcl/ant/internal/engine/colony"
	"github.com/gitpcl/ant/internal/engine/config"
	"github.com/gitpcl/ant/internal/engine/species"
	store "github.com/gitpcl/ant/internal/engine/store"
	"github.com/gitpcl/ant/internal/engine/telemetry"
	"github.com/gitpcl/ant/internal/engine/verify"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newFixCmd builds `ant fix [path] [--ant ...] [--apply]` (TECHSPEC §7). It runs
// the colony and renders the live colony view; verified diffs land in staging.
// Nothing is applied without --apply, and --apply auto-lands ONLY species whose
// effective auto_apply is true (ADR-0002). This handler is a thin front door:
// it parses flags, resolves config + species + recipes (all engine packages),
// and calls colony.Drive — the engine owns the bus, the TUI/JSON renderers, the
// worker pool, and the go-git apply (the boundary test keeps that out of here).
func newFixCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fix [path]",
		Short: "Produce verified staged diffs (apply only with --apply)",
		Long: "Fix runs the colony — detect, fix, verify — and stages the verified " +
			"diffs for `ant review`. With --apply it also lands diffs from trusted " +
			"species (effective auto_apply true) on a branch.",
		Args: cobra.ArbitraryArgs,
		RunE: runFix,
	}
	cmd.Flags().StringSlice("ant", nil, "limit the run to the named species (repeatable)")
	cmd.Flags().Bool("apply", false, "fuse apply for trusted species (effective auto_apply true)")
	cmd.Flags().Bool("no-branch", false, "with --apply, land on the current branch instead of a new one")
	cmd.Flags().Bool("ascii", false, "use ASCII glyphs instead of Unicode (also honors NO_COLOR)")
	cmd.Flags().Bool("trails", false, "bias scheduling by trail density and write trail markers (default off; ADR-0003)")
	return cmd
}

// runFix is the fix handler. It loads config, resolves the enabled species,
// builds the per-species fix/verify/trust recipes + detectors via the engine's
// composition root, and drives the run with the renderer chosen by TTY/--json.
func runFix(cmd *cobra.Command, args []string) error {
	// Build the single config.Resolver that owns precedence (flags > ant.toml >
	// manifest > built-in default — TECHSPEC §9). config.Bind reads ant.toml into
	// viper's config-file band and binds the bound pflags; NewResolver seeds the
	// defaults/manifest band. Every effective knob this run uses (verify limits,
	// model, concurrency) is read back through this one authority — runFix no
	// longer calls verify.DefaultLimits() or its own flag-reading helpers, so the
	// front door cannot drift from the resolver other front doors share.
	v, _, err := config.Bind(cmd.Flags(), configPathFlag(cmd))
	if err != nil {
		return err // malformed ant.toml → operational (exit 2)
	}
	resolver := config.NewResolver(v, config.ManifestDefaults{})

	cfg, _, err := config.Load(configFileOrDefault(configPathFlag(cmd)))
	if err != nil {
		return err // malformed ant.toml → operational (exit 2)
	}

	path := "."
	if len(args) > 0 && args[0] != "" {
		path = args[0]
	}
	antFilter := stringSlice(cmd, "ant")

	resolved, err := species.NewResolver(userSpeciesRoot, nil).Resolve(cfg)
	if err != nil {
		return err // a malformed species manifest → operational (exit 2)
	}

	// Trust authority: fold the freshly-installed propose-only override
	// (TECHSPEC §6.3) on top of Sprint-004's ant.toml-or-manifest auto_apply,
	// reading persisted install/review state from the Store. The resulting
	// decisions carry the FINAL effective auto-apply the colony --apply path
	// gates on, so a freshly-installed species cannot auto-land on its first run.
	st := store.New(path)
	decisions, err := species.ResolveTrust(resolved, st)
	if err != nil {
		return err // operational (exit 2): cannot read trust state
	}

	rc := colony.RecipeConfig{
		// Diff-bounded caps and the rawmodel model id all come from the resolver
		// (flags > ant.toml > manifest > default), so [verify].max_changed_lines/
		// max_changed_files and [colony].model are honored without a second code
		// path. A configured 0 limit means "unbounded" on that dimension (verify
		// .Limits semantics) — the resolver passes the value through unchanged.
		Limits: verify.Limits{
			MaxChangedLines: resolver.MaxChangedLines(),
			MaxChangedFiles: resolver.MaxChangedFiles(),
		},
		// Effective fixer adapter for llm species (flag > ant.toml > manifest >
		// default), selected in the colony composition root — cmd/ant only threads
		// the resolved name (TECHSPEC §3). An unknown value surfaces as a typed
		// config error from BuildRecipes, never a silent rawmodel fallback.
		Fixer:            resolver.Fixer(),
		RawModelModel:    resolver.Model(),
		RawModelEndpoint: os.Getenv("ANT_RAWMODEL_ENDPOINT"),
		RawModelAPIKey:   os.Getenv("ANT_RAWMODEL_API_KEY"),
	}

	// Materialize the embedded built-in rule files so the ast-grep detector (a
	// shell-out plugin boundary, TECHSPEC §2) can read them; the engine owns the
	// extraction. User species already live on disk and resolve in place.
	rulesRoot, cleanupRules, err := species.MaterializeBuiltinRules()
	if err != nil {
		return err // operational (exit 2): cannot stage built-in rules
	}
	defer cleanupRules()

	recipes, detectors, err := colony.BuildRecipes(decisions, antFilter, rulesRoot, rc)
	if err != nil {
		return err
	}

	// Species that will participate in this run — recorded as "seen on a previous
	// run" after it completes, so the freshly-installed override tracks install
	// state across runs. This is exactly the recipe set (enabled + --ant-filtered).
	seen := make([]string, 0, len(recipes))
	for name := range recipes {
		seen = append(seen, name)
	}

	renderer := colony.RendererTUI
	if boolFlag(cmd, "json") || !isTTY() {
		renderer = colony.RendererJSON // colony-view.md §5: --json or non-TTY → machine stream
	}

	opts := colony.DriveOptions{
		Scope:       engine.Scope{Root: path, Species: antFilter, IgnoreGlobs: resolver.IgnorePaths()},
		Detectors:   detectors,
		Recipes:     recipes,
		Store:       st,
		Concurrency: resolver.Concurrency(),
		Renderer:    renderer,
		Workers:     resolver.Concurrency(),
		Ascii:       asciiEnabled(cmd),
		Color:       colorEnabled(),
		SeenSpecies: seen,
		SeenMarker:  st,
	}

	if boolFlag(cmd, "apply") {
		opts.ApplyFused = true
		opts.Apply = apply.NewApplier(apply.Options{Root: path, NoBranch: boolFlag(cmd, "no-branch")})
	}

	// Trails (ADR-0003): flag > ant.toml [colony] trails > default off. Only when
	// enabled does the colony bias scheduling by trail density and write markers;
	// otherwise opts.Trails stays nil and the run is order-stable. The same local
	// Store backs trail state — the seam the enterprise shared-trail layer reuses.
	if cfg.ResolveTrails(cmd.Flags().Changed("trails"), boolFlag(cmd, "trails")) {
		opts.Trails = st
	}

	// Telemetry (PRD §8): OFF by default. telemetry.New returns a nil/no-op sink
	// unless [telemetry] enabled = true, in which case it observes this run's bus
	// as a passive consumer and folds privacy-safe aggregates (species usage,
	// verifier catch rate). It never gates the run or touches the --json contract.
	// Close flushes the aggregate Report through the (v1 no-op) transport; a
	// telemetry error must never break the fix run, so it is intentionally ignored.
	tel := telemetry.New(cfg.ResolveTelemetry(), telemetry.NopTransport{}, nil)
	opts.Telemetry = tel
	defer func() { _ = tel.Close() }()

	_, err = colony.Drive(cmd.Context(), cmd.OutOrStdout(), opts)
	return err
}

// userSpeciesRoot is where on-disk user species are discovered (TECHSPEC §6.3).
const userSpeciesRoot = ".ant/species"

// isTTY reports whether stdout is an interactive terminal. The TUI is attached
// only when it is; piped/redirected/CI output gets the --json stream instead
// (colony-view.md §5). This is a single boundary check, not business logic.
func isTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// asciiEnabled reports whether to use ASCII glyphs: the --ascii flag, or NO_COLOR
// set (a no-color terminal is often also a limited one) — colony-view.md §6.
func asciiEnabled(cmd *cobra.Command) bool {
	if boolFlag(cmd, "ascii") {
		return true
	}
	return !colorEnabled()
}

// colorEnabled reports whether ANSI color may be emitted. NO_COLOR (any value)
// disables it; a non-TTY also disables it since the machine stream is plain.
func colorEnabled() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	return isTTY()
}
