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

	// DefaultScriptInterpreter is the interpreter the command detector / command:
	// verifier use when a manifest declares no [detector].interpreter /
	// [verify].interpreter. POSIX "sh" is the portable default; the binary is
	// resolved from PATH at run time and ALWAYS exec'd in argv form (never via a
	// shell string), so a script path can never be interpreted as a shell command.
	DefaultScriptInterpreter = "sh"

	// FixKindDeterministic selects a code-transform fixer with no LLM.
	FixKindDeterministic = "deterministic"
	// FixKindLLM selects an LLM-assisted fixer that requires a prompt.
	FixKindLLM = "llm"
	// FixKindTool selects the tool-runner fixer: it execs a manifest-declared
	// external formatter/autofixer (gofmt, prettier, ruff, eslint, clippy) on a
	// scratch copy and captures the diff (Sprint 017, TECHSPEC §10). The command +
	// args are declarative in [fix] (Command/Args), so no tool is special-cased in
	// the engine.
	FixKindTool = "tool"
	// FixKindNone declares a REPORT-ONLY species (Sprint 022 Finding 4): it
	// detects and REPORTS findings but proposes NO change. A none-kind manifest
	// needs NO [fix].transform/prompt and NO [verify].checks — there is nothing to
	// fix and nothing to verify — so the loader does not require that boilerplate
	// (it previously forced a fake deterministic [fix] + detector-clears [verify]
	// workaround, the Sprint 019 ENGINE-GAP hack todo-expired carried). `ant scout`
	// reports a none-kind species with zero working-tree writes; `ant fix` rejects
	// it with a clear "report-only" message because it declares nothing to fix.
	FixKindNone = "none"

	// ManifestSchemaVersion is the baseline version of the species.toml schema
	// (Sprint 022 Future-Proofing #4). A manifest that omits schema_version is
	// inferred to be this baseline (SchemaVersion()), so every species authored
	// before the field existed still loads unchanged. Bump policy mirrors the
	// --json events.SchemaVersion: ADDING an optional field is backward-compatible
	// and does not bump it; renaming/removing/repurposing an existing field is
	// breaking and MUST increment it. Recorded in .harness/progress_log.md so the
	// next bump is deliberate; golden-tested so an accidental bump fails CI.
	ManifestSchemaVersion = "1"
)

// Manifest is the decoded species.toml document (TECHSPEC §6.2). It is the
// typed, validated view of a single species folder. The Detect/Fix/Verify
// sub-structs mirror the [detector], [fix], and [verify] sections.
//
// Source records where the manifest was loaded from (an embedded path or an
// on-disk directory) so resolution can report provenance and so user species
// can be distinguished from built-ins; it is not part of the TOML.
type Manifest struct {
	// SchemaVersion is the machine-readable species.toml schema version (Sprint
	// 022 Future-Proofing #4). It lets `ant species validate`, the registry, and
	// future tooling detect a manifest authored against a breaking schema change.
	// Optional: an unset value is inferred to ManifestSchemaVersion by
	// SchemaVersion(), so pre-existing manifests (none declare it) keep loading.
	SchemaVersion string `toml:"schema_version"`

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

	// Capability metadata (Sprint 022 Future-Proofing #3) declares what running
	// this species needs, so front doors, `ant doctor`, `ant species validate`,
	// and the capability-matrix doc can read one authority instead of
	// re-deriving it. Every field has an inferred default the loader fills when
	// the manifest leaves it unset (Capabilities()); an explicit manifest value
	// always wins. Pointers distinguish "unset → infer" from an explicit value
	// (mirroring AutoApply/Enabled), so an author can, e.g., force
	// requires_network=false on a species the loader would otherwise infer true.
	//
	//   - RequiresExec: the species execs an external process during scan/fix
	//     (a command detector script or a tool-runner fix). Inferred from
	//     detector kind=command OR fix kind=tool.
	//   - RequiresNetwork: the species reaches the network (an LLM fix calls a
	//     model endpoint). Inferred from fix kind=llm.
	//   - RequiresTool: the name of the external binary the species needs on PATH
	//     (e.g. "ast-grep", "gofmt", "goimports", "ruff"). Inferred from the
	//     ast-grep detector ("ast-grep") or a tool fix's [fix].command.
	//   - ReportOnly: the species reports findings but proposes no change.
	//     Inferred from fix kind=none (IsReportOnly).
	RequiresExec    *bool   `toml:"requires_exec"`
	RequiresNetwork *bool   `toml:"requires_network"`
	RequiresTool    *string `toml:"requires_tool"`
	ReportOnly      *bool   `toml:"report_only"`

	// Source is the loaded provenance (e.g. "embed:species/unused-import" or a
	// ".ant/species/unused-import" directory). Not decoded from TOML.
	Source string `toml:"-"`
}

// EffectiveSchemaVersion returns the resolved species.toml schema version: the
// explicit schema_version when the manifest declares one, the inferred
// ManifestSchemaVersion baseline otherwise. Inference keeps every manifest
// authored before the field existed valid and at the baseline, mirroring how
// Capabilities() infers unset metadata. (Named distinctly from the
// SchemaVersion field, which holds the raw decoded value.)
func (m Manifest) EffectiveSchemaVersion() string {
	if m.SchemaVersion != "" {
		return m.SchemaVersion
	}
	return ManifestSchemaVersion
}

// Capabilities is the resolved capability metadata for a species: the explicit
// manifest values where set, the inferred defaults otherwise (Sprint 022
// Future-Proofing #3). It is what front doors, `ant doctor`, `ant species
// validate`, and the capability-matrix doc read, so the inference rules live in
// one place rather than each consumer re-deriving them from kinds.
// JSON tags mirror the manifest's snake_case capability field names
// (requires_exec/network/tool, report_only) so the `ant species validate`
// --json document is self-describing in the same vocabulary an author writes in
// the manifest, and consistent with the document's other snake_case keys. No
// other consumer serializes Capabilities, so these tags are additive.
type Capabilities struct {
	RequiresExec    bool   `json:"requires_exec"`
	RequiresNetwork bool   `json:"requires_network"`
	RequiresTool    string `json:"requires_tool,omitempty"`
	ReportOnly      bool   `json:"report_only"`
}

// Capabilities computes the effective capability metadata for the manifest:
// each explicit field overrides its inferred default. The inference is derived
// from the already-validated detector/fix kinds, so a manifest that declares no
// capability metadata still reports accurate capabilities.
func (m Manifest) Capabilities() Capabilities {
	c := Capabilities{
		RequiresExec:    m.inferRequiresExec(),
		RequiresNetwork: m.Fix.Kind == FixKindLLM,
		RequiresTool:    m.inferRequiresTool(),
		ReportOnly:      m.IsReportOnly(),
	}
	if m.RequiresExec != nil {
		c.RequiresExec = *m.RequiresExec
	}
	if m.RequiresNetwork != nil {
		c.RequiresNetwork = *m.RequiresNetwork
	}
	if m.RequiresTool != nil {
		c.RequiresTool = *m.RequiresTool
	}
	if m.ReportOnly != nil {
		c.ReportOnly = *m.ReportOnly
	}
	return c
}

// inferRequiresExec reports whether the species execs an external process by
// default: a command detector runs a script (DetectKindCommand) and a tool fix
// runs an external formatter/autofixer (FixKindTool). An ast-grep detector also
// shells out to ast-grep, but that is captured by RequiresTool rather than the
// generic "runs an arbitrary script" exec flag, matching how the command
// detector is the trust-gated escape hatch.
func (m Manifest) inferRequiresExec() bool {
	return m.Detector.Kind == DetectKindCommand || m.Fix.Kind == FixKindTool
}

// inferRequiresTool names the external binary the species needs on PATH by
// default: an ast-grep detector needs "ast-grep"; a tool fix needs its declared
// [fix].command (gofmt/goimports/ruff/…). The tool fix takes precedence because
// it is the binary the fix actually execs; absent either, no tool is required.
func (m Manifest) inferRequiresTool() string {
	if m.Fix.Kind == FixKindTool && m.Fix.Command != "" {
		return m.Fix.Command
	}
	if m.Detector.Kind == DetectKindASTGrep {
		return DetectKindASTGrep
	}
	return ""
}

// Detect is the [detector] section: which detector kind runs and the rule (or
// script) it references (TECHSPEC §6.2).
type Detect struct {
	Kind string `toml:"kind"` // ast-grep | command
	Rule string `toml:"rule"` // rule file (ast-grep) — relative to the species folder
	// Script is the script to run for kind=command (script escape hatch),
	// relative to the species folder. Interpreter is the interpreter binary that
	// runs it (argv form: <interpreter> <script> <scope-root>) — resolved from
	// PATH at scan time, NEVER via a shell. Empty Interpreter defaults to "sh"
	// (POSIX shell) so a portable detect.sh needs no override; a species may set
	// "bash"/"python3"/etc. Required-field rules live in the loader.
	Script      string `toml:"script"`
	Interpreter string `toml:"interpreter"`
}

// Fix is the [fix] section: the fix strategy and its parameters (TECHSPEC §6.2).
// An llm fix requires Prompt; a deterministic fix names a Transform and does
// NOT require a prompt; a tool fix declares a Command (+ optional Args/Timeout/
// VersionArgs) to exec; a none fix (report-only) declares nothing — no
// Transform, no Prompt, no Command.
type Fix struct {
	Kind      string `toml:"kind"`      // llm | deterministic | tool | none
	Prompt    string `toml:"prompt"`    // prompt file — required for kind=llm
	Transform string `toml:"transform"` // transform name — for kind=deterministic

	// Command/Args/Timeout/VersionArgs declare the external command the
	// tool-runner execs (kind=tool, Sprint 017). The command is resolved from PATH
	// at fix time; Args may contain the "{file}" placeholder the runner
	// substitutes with the scratch copy's path (an Args list with no placeholder
	// appends the file). Timeout is a Go duration string ("30s"); empty uses the
	// engine default. VersionArgs (e.g. ["--version"]) is an optional best-effort
	// version probe for provenance. Required for kind=tool, ignored otherwise.
	Command     string   `toml:"command"`
	Args        []string `toml:"args"`
	Timeout     string   `toml:"timeout"`
	VersionArgs []string `toml:"version_args"`
}

// Verify is the [verify] section: the ordered list of verifier checks
// (TECHSPEC §6.2). Entries are built-in kinds (compile, tests:affected,
// detector-clears, diff-bounded, formatter-idempotence, …) or a command escape
// hatch ("command:verify.sh").
//
// Tool declares the formatter the formatter-idempotence check re-runs ([verify.tool]
// in the manifest). It mirrors the tool-runner's command/args so a species names
// the formatter once for the fix and once for the idempotence gate; empty unless
// the checks include formatter-idempotence.
type Verify struct {
	Checks []string `toml:"checks"`
	Tool   ToolRef  `toml:"tool"`
	// Interpreter is the interpreter binary that runs every "command:<script>"
	// check in Checks (argv form: <interpreter> <script>, run in the scratch
	// copy's root) — resolved from PATH at verify time, NEVER via a shell. Empty
	// defaults to "sh"; a species may set "bash"/"python3"/etc. One interpreter
	// covers all command: checks a species declares (they share a language).
	Interpreter string `toml:"interpreter"`
}

// ToolRef is the [verify.tool] section: the command + args the
// formatter-idempotence verifier re-runs over the post-fix tree (Sprint 017). It
// is the same declarative shape as the [fix] tool command, kept separate so the
// idempotence gate is wired independently of the fixer that produced the diff.
type ToolRef struct {
	Command string   `toml:"command"`
	Args    []string `toml:"args"`
}

// EffectiveAutoApply reports the manifest's author-suggested auto_apply default,
// treating an unset value as false (the safe default — TECHSPEC §6.3). The
// ant.toml override is layered on top of this by resolution, not here.
func (m Manifest) EffectiveAutoApply() bool {
	return m.AutoApply != nil && *m.AutoApply
}

// IsReportOnly reports whether the species is report-only (fix kind "none",
// Sprint 022 Finding 4): it detects and reports findings but proposes no change.
// `ant fix` rejects such a species (it declares nothing to fix); `ant scout`
// reports it normally. Kept on the manifest so both front doors and the recipe
// builder ask the same authority rather than comparing the kind string inline.
func (m Manifest) IsReportOnly() bool {
	return m.Fix.Kind == FixKindNone
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
