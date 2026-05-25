// Package config owns ant.toml: the typed schema (TECHSPEC §9), a strict loader
// that surfaces unknown keys as warnings, and the resolution chain
// (flags > ant.toml > species manifest > built-in defaults). It lives in the
// engine — not cmd/ant — so every front door resolves config identically and so
// the boundary test (TECHSPEC §3) keeps config logic out of the thin CLI. The
// CLI only reads the resolved values and translates typed errors to exit codes.
package config

import "runtime"

// Built-in default keys and values (TECHSPEC §9). These are the lowest layer of
// the resolution order — overridden by the species manifest, then ant.toml,
// then flags. They are the values a bare `ant` uses with no config present.
const (
	// KeyConcurrency is the viper key for the parallel-ant count.
	KeyConcurrency = "colony.concurrency"
	// KeyFixer is the viper key for the default fixer adapter.
	KeyFixer = "colony.fixer"
	// KeyModel is the viper key for the default model id.
	KeyModel = "colony.model"
	// KeyIgnorePaths is the viper key for the ignore-globs list.
	KeyIgnorePaths = "ignore.paths"
	// KeyVerifyMaxChangedLines is the viper key for the diff-bounded line cap.
	KeyVerifyMaxChangedLines = "verify.max_changed_lines"
	// KeyVerifyMaxChangedFiles is the viper key for the diff-bounded file cap.
	KeyVerifyMaxChangedFiles = "verify.max_changed_files"
)

// Diff-bounded built-in defaults (TECHSPEC §5.3 — diff-bounded guards runaway
// edits). They are the sane caps used when ant.toml omits a [verify] section.
// The values mirror verify.DefaultMaxChanged* but live here so config has no
// import on verify (the dependency runs config → verify at the call site, never
// the reverse).
const (
	// DefaultMaxChangedLines caps total added+removed diff lines (diff-bounded).
	DefaultMaxChangedLines = 200
	// DefaultMaxChangedFiles caps files touched by a single fix (diff-bounded).
	DefaultMaxChangedFiles = 10
)

// DefaultFixer is the built-in default fixer adapter (TECHSPEC §9 example).
const DefaultFixer = "pi"

// DefaultModel is the built-in default model id (TECHSPEC §9 example). It is a
// config value, never a hardcoded runtime constant (TECHSPEC §2 — model stays
// configurable).
const DefaultModel = "qwen2.5-coder"

// DefaultConcurrency returns the built-in default ant count: NumCPU
// (TECHSPEC §8 step 3, §9). It is a function, not a const, because it reads the
// host's CPU count at call time.
func DefaultConcurrency() int {
	if n := runtime.NumCPU(); n > 0 {
		return n
	}
	return 1
}

// Config is the decoded ant.toml document (TECHSPEC §9). It is the typed view of
// the file used for schema validation and unknown-key detection; effective
// values for a run are read through the Resolver, which layers flags and
// defaults on top of this. Unknown top-level sections or keys are reported as
// warnings by the loader rather than silently dropped.
type Config struct {
	Colony    Colony             `toml:"colony"`
	Ignore    Ignore             `toml:"ignore"`
	Verify    Verify             `toml:"verify"`
	Telemetry Telemetry          `toml:"telemetry"`
	Species   map[string]Species `toml:"species"`
}

// Telemetry holds the [telemetry] section: the single opt-in switch for the
// privacy-respecting aggregate metrics (PRD §8, TECHSPEC §11). Telemetry is OFF
// by default — with no [telemetry] section, or with Enabled nil/false, the
// engine collects and sends NOTHING. Setting `[telemetry] enabled = true` is the
// explicit, documented opt-in that turns on aggregate collection (species usage,
// accept rate, verifier catch rate — never code/paths/PII). The pointer
// distinguishes "absent" (default off) from an explicit false, matching the
// schema's absent-vs-zero discipline; there is deliberately no endpoint/key
// field here (v1 has no live transport — see internal/engine/telemetry).
type Telemetry struct {
	Enabled *bool `toml:"enabled"`
}

// DefaultTelemetry is the built-in default for telemetry: OFF (PRD §8 — privacy
// is the contract; telemetry is opt-in only). It is the value a bare `ant` uses
// with no config: zero collection, zero send.
const DefaultTelemetry = false

// ResolveTelemetry computes the effective telemetry setting through the
// resolution chain (ant.toml > built-in default). It lives in the engine — not
// the CLI — so every front door resolves telemetry identically and the boundary
// test keeps the logic out of the thin command layer (TECHSPEC §3). There is no
// flag override on purpose: telemetry is a deliberate, persistent opt-in in
// ant.toml, not something silently flipped on by a transient command-line flag.
// With the [telemetry] enabled key absent it falls through to DefaultTelemetry
// (off), so the default posture is unambiguously zero collection.
func (c Config) ResolveTelemetry() bool {
	if c.Telemetry.Enabled != nil {
		return *c.Telemetry.Enabled
	}
	return DefaultTelemetry
}

// Verify holds the [verify] section: the diff-bounded size caps that guard
// runaway edits (TECHSPEC §5.3, §8.1). Pointer fields distinguish "absent" (fall
// through to the built-in default) from "set to 0" (unbounded on that
// dimension), matching the rest of the schema's absent-vs-zero discipline.
type Verify struct {
	MaxChangedLines *int `toml:"max_changed_lines"`
	MaxChangedFiles *int `toml:"max_changed_files"`
}

// Colony holds the [colony] section: run-wide knobs (TECHSPEC §9). Pointer
// fields distinguish "absent" (nil) from "set to the zero value" so the
// resolver only overrides lower layers when a key is actually present.
type Colony struct {
	Concurrency *int    `toml:"concurrency"`
	Fixer       *string `toml:"fixer"`
	Model       *string `toml:"model"`
	// Trails opts into trail-density scheduler re-prioritization (ADR-0003,
	// TECHSPEC §8.2). It is OFF by default: with Trails nil or false the colony
	// schedules embarrassingly-parallel and order-stable, exactly as v1 ships, and
	// writes no trail markers. Setting it true makes a verified-fixing ant write a
	// trail marker (keyed by species + code-location class) and biases the work
	// queue toward classes with higher trail density. Pointer distinguishes
	// "absent" (default off) from an explicit false, matching the section's
	// absent-vs-zero discipline.
	Trails *bool `toml:"trails"`
}

// DefaultTrails is the built-in default for trail-density scheduling: OFF
// (ADR-0003 — v1 ships embarrassingly-parallel; trails are opt-in behind a
// flag). It is the value a bare `ant` uses with no config and no --trails flag.
const DefaultTrails = false

// ResolveTrails computes the effective trails setting through the resolution
// chain (flag > ant.toml > built-in default). It lives in the engine — not the
// CLI — so every front door resolves trails identically and the boundary test
// keeps the logic out of the thin command layer (TECHSPEC §3). flagSet reports
// whether the --trails flag was explicitly provided; flagValue is its value.
// With the flag absent it falls through to [colony] trails, then to
// DefaultTrails (off). This mirrors the absent-vs-zero pointer discipline of the
// rest of the schema: an unset flag and an unset toml key both mean "fall
// through", never "force off".
func (c Config) ResolveTrails(flagSet, flagValue bool) bool {
	if flagSet {
		return flagValue
	}
	if c.Colony.Trails != nil {
		return *c.Colony.Trails
	}
	return DefaultTrails
}

// Ignore holds the [ignore] section: path globs excluded from a run
// (TECHSPEC §9).
type Ignore struct {
	Paths []string `toml:"paths"`
}

// Species holds a [species.<name>] section: the project's per-species overrides
// (TECHSPEC §9, §6.3). AutoApply overrides the manifest's author-suggested
// default; Enabled toggles a species on/off (e.g. ai-slop ships disabled and is
// enabled here). Pointers distinguish absent from false so an unset key falls
// through to the manifest/default layer rather than forcing false.
type Species struct {
	AutoApply *bool `toml:"auto_apply"`
	Enabled   *bool `toml:"enabled"`
}

// SpeciesConfig returns the per-species override section by name. The bool
// reports whether a [species.<name>] section was present at all, so callers can
// distinguish "no override → fall through to manifest" from "override present
// with unset fields". It never panics on a nil map.
func (c Config) SpeciesConfig(name string) (Species, bool) {
	if c.Species == nil {
		return Species{}, false
	}
	s, ok := c.Species[name]
	return s, ok
}
