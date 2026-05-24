// Package species owns the declarative species subsystem (TECHSPEC §6): the
// species.toml model + loader, the kind→adapter registry, and the
// resolution/override logic that merges embedded built-in species with the
// user's .ant/species/ tree. It lives in internal/engine so every front door
// resolves species identically and so the boundary test (TECHSPEC §3) keeps all
// species logic out of the thin cmd/ant layer.
package species

import "github.com/gitpcl/ant/internal/engine"

// Detect/Fix/Verify kind tokens. These are the closed set the registry
// dispatches on (TECHSPEC §6.2). Kept as named constants so the loader,
// validator, and registry agree on one spelling rather than scattering string
// literals across the package.
const (
	// DetectKindASTGrep selects the ast-grep detector adapter (default).
	DetectKindASTGrep = "ast-grep"
	// DetectKindCommand selects the command (script escape-hatch) detector.
	DetectKindCommand = "command"

	// FixKindDeterministic selects a code-transform fixer with no LLM.
	FixKindDeterministic = "deterministic"
	// FixKindLLM selects an LLM-assisted fixer that requires a prompt.
	FixKindLLM = "llm"
)

// Manifest is the decoded species.toml document (TECHSPEC §6.2). It is the
// typed, validated view of a single species folder. The Detect/Fix/Verify
// sub-structs mirror the [detector], [fix], and [verify] sections.
//
// Source records where the manifest was loaded from (an embedded path or an
// on-disk directory) so resolution can report provenance and so user species
// can be distinguished from built-ins; it is not part of the TOML.
type Manifest struct {
	Name        string   `toml:"name"`
	Description string   `toml:"description"`
	Severity    string   `toml:"severity"`
	Languages   []string `toml:"languages"`

	// AutoApply is the author-suggested default; ant.toml overrides it
	// (TECHSPEC §6.3, ADR-0002). Pointer distinguishes "unset" (fall through to
	// the built-in default of false) from an explicit false.
	AutoApply *bool `toml:"auto_apply"`
	// Enabled toggles the species on/off. Pointer distinguishes "unset"
	// (defaults to enabled) from an explicit false (e.g. ai-slop ships disabled).
	Enabled *bool `toml:"enabled"`

	// Detector is the canonical [detector] section (TECHSPEC §6.2). Detect is an
	// accepted alias ([detect]) collapsed into Detector by the loader so both
	// spellings work; only Detector is consulted after loading.
	Detector Detect `toml:"detector"`
	Detect   Detect `toml:"detect"`

	Fix    Fix    `toml:"fix"`
	Verify Verify `toml:"verify"`

	// Source is the loaded provenance (e.g. "embed:species/unused-import" or a
	// ".ant/species/unused-import" directory). Not decoded from TOML.
	Source string `toml:"-"`
}

// Detect is the [detector] section: which detector kind runs and the rule (or
// script) it references (TECHSPEC §6.2).
type Detect struct {
	Kind string `toml:"kind"` // ast-grep | command
	Rule string `toml:"rule"` // rule file (ast-grep) — relative to the species folder
	// Script is the command to run for kind=command (script escape hatch).
	Script string `toml:"script"`
}

// Fix is the [fix] section: the fix strategy and its parameters (TECHSPEC §6.2).
// An llm fix requires Prompt; a deterministic fix names a Transform and does
// NOT require a prompt.
type Fix struct {
	Kind      string `toml:"kind"`      // llm | deterministic
	Prompt    string `toml:"prompt"`    // prompt file — required for kind=llm
	Transform string `toml:"transform"` // transform name — for kind=deterministic
}

// Verify is the [verify] section: the ordered list of verifier checks
// (TECHSPEC §6.2). Entries are built-in kinds (compile, tests:affected,
// detector-clears, diff-bounded, …) or a command escape hatch
// ("command:verify.sh").
type Verify struct {
	Checks []string `toml:"checks"`
}

// EffectiveAutoApply reports the manifest's author-suggested auto_apply default,
// treating an unset value as false (the safe default — TECHSPEC §6.3). The
// ant.toml override is layered on top of this by resolution, not here.
func (m Manifest) EffectiveAutoApply() bool {
	return m.AutoApply != nil && *m.AutoApply
}

// IsEnabled reports whether the species is enabled, defaulting an unset value to
// true (TECHSPEC §6.2). Only an explicit enabled=false (e.g. ai-slop) disables a
// species at the manifest layer.
func (m Manifest) IsEnabled() bool {
	return m.Enabled == nil || *m.Enabled
}

// ParsedSeverity converts the manifest's severity token to the engine Severity
// enum, going through the same boundary check every other severity input uses
// (engine.ParseSeverity). An empty or invalid token is rejected by the loader's
// validation, so callers that reach this on a validated manifest get a real
// level.
func (m Manifest) ParsedSeverity() (engine.Severity, error) {
	return engine.ParseSeverity(m.Severity)
}
