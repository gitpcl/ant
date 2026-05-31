package species

import (
	"testing"

	"github.com/gitpcl/ant/internal/engine/config"
	builtins "github.com/gitpcl/ant/species"
)

// adr0002 pins each built-in species' fix strategy and trust defaults from
// ADR-0002 (docs/decisions/0002-launch-species.md). The embed test asserts the
// embedded manifests match this table exactly, so a drift in any built-in
// manifest is caught here.
var adr0002 = map[string]struct {
	fixKind   string
	autoApply bool
	enabled   bool
}{
	"unused-import":            {FixKindDeterministic, true, true},
	"dead-code":                {FixKindDeterministic, true, true},
	"unused-variable":          {FixKindDeterministic, true, true},
	"redundant-conversion":     {FixKindDeterministic, true, true},
	"unreachable-code":         {FixKindDeterministic, true, true},
	"empty-block":              {FixKindDeterministic, false, true},
	"duplicate-condition":      {FixKindDeterministic, false, true},
	"redundant-nil-check":      {FixKindDeterministic, false, true},
	"ineffective-assignment":   {FixKindDeterministic, false, true},
	"formatter-drift":          {FixKindTool, true, true},
	"import-sort":              {FixKindTool, true, true},
	"lint-autofix":             {FixKindTool, true, true},
	"trailing-debug-code":      {FixKindDeterministic, false, true},
	"n+1-query":                {FixKindLLM, false, true},
	"missing-await":            {FixKindLLM, false, true},
	"nil-deref":                {FixKindLLM, false, true},
	"ai-slop":                  {FixKindLLM, false, false},
	"ignored-error":            {FixKindLLM, false, true},
	"unchecked-type-assertion": {FixKindLLM, false, true},
	"resource-leak":            {FixKindLLM, false, true},
	"missing-context-timeout":  {FixKindLLM, false, true},
	"unsafe-concurrency":       {FixKindLLM, false, true},
	"sql-string-concat":        {FixKindLLM, false, true},
	"deep-nesting":             {FixKindLLM, false, true},
	"long-function":            {FixKindLLM, false, true},
	"magic-number":             {FixKindLLM, false, true},
	"duplicate-code-small":     {FixKindLLM, false, true},
	// todo-expired is REPORT-ONLY and ships DISABLED by default. As of Sprint 022
	// (Finding 4) report-only is a FIRST-CLASS manifest kind: its [fix].kind="none"
	// (no fake deterministic [fix] + detector-clears [verify] workaround — that was
	// the Sprint 019 ENGINE-GAP #2 hack, now removed). It declares nothing to fix,
	// so `ant fix` rejects it; it is surfaced only via scout and resolves disabled.
	// Hence the row is {FixKindNone, auto_apply=false, enabled=false}.
	"todo-expired": {FixKindNone, false, false},
	// Sprint 020 P5 dependency/config: four propose-only (auto_apply=false)
	// deterministic species that operate on non-source files (go.mod, config.json,
	// CI YAML) via the command-detector + command:-verifier escape hatches. Their
	// fix is a delete-match removal/normalization; they ship enabled, propose-only.
	"unused-dependency":    {FixKindDeterministic, false, true},
	"stale-dependency-pin": {FixKindDeterministic, false, true},
	"dead-config":          {FixKindDeterministic, false, true},
	"duplicate-ci-step":    {FixKindDeterministic, false, true},

	// Sprint 021 P6 security-hygiene: three SECURITY-stage, propose-only
	// (auto_apply=false) species whose value is the verified remediation. All
	// ship enabled, propose-only. hardcoded-secret has an llm fix (env-var
	// rewrite + .env.example), gated by compile + a command: secret-scanner-clears
	// verifier + detector-clears; insecure-random and unsafe-temp-file have llm
	// fixes gated by compile + tests:affected + detector-clears.
	"hardcoded-secret": {FixKindLLM, false, true},
	"insecure-random":  {FixKindLLM, false, true},
	"unsafe-temp-file": {FixKindLLM, false, true},
}

// TestEmbed_BuiltinsDiscoverableNoDisk is the core feature-3 assertion: the
// built-in species load from the EMBEDDED FS with no on-disk species/ directory
// present at runtime. The resolver is given an empty userRoot, so the only
// source is builtins.FS(). The expected set is the adr0002 table above.
func TestEmbed_BuiltinsDiscoverableNoDisk(t *testing.T) {
	// userRoot "" => the resolver never touches the disk; built-ins come purely
	// from the embedded tree compiled into the test binary.
	r := NewResolver("", NewRegistry())
	resolved, err := r.Resolve(config.Config{})
	if err != nil {
		t.Fatalf("Resolve built-ins from embed: %v", err)
	}

	got := map[string]Resolved{}
	for _, rv := range resolved {
		got[rv.Manifest.Name] = rv
		if rv.Origin != OriginBuiltin {
			t.Errorf("%s: Origin = %v, want builtin", rv.Manifest.Name, rv.Origin)
		}
	}

	if len(got) != len(adr0002) {
		t.Fatalf("resolved %d built-in species, want %d: got %v", len(got), len(adr0002), keysOf(got))
	}

	for name, want := range adr0002 {
		rv, ok := got[name]
		if !ok {
			t.Errorf("built-in species %q missing from embedded tree", name)
			continue
		}
		if rv.Manifest.Fix.Kind != want.fixKind {
			t.Errorf("%s: fix kind = %q, want %q (ADR-0002)", name, rv.Manifest.Fix.Kind, want.fixKind)
		}
		if rv.EffectiveAutoApply != want.autoApply {
			t.Errorf("%s: effective auto_apply = %v, want %v (ADR-0002, no ant.toml override)", name, rv.EffectiveAutoApply, want.autoApply)
		}
		if rv.EffectiveEnabled != want.enabled {
			t.Errorf("%s: effective enabled = %v, want %v (ADR-0002)", name, rv.EffectiveEnabled, want.enabled)
		}
	}
}

// TestAISlopShipsDisabled is the ai-slop feature's core assertion against the
// REAL embedded species (not a synthetic fixture): on a default run (no ant.toml
// opt-in) the embedded ai-slop resolves DISABLED, so the colony's recipe builder
// excludes it (it cannot run); and an explicit ant.toml [species.ai-slop]
// enabled=true flips EffectiveEnabled on, the only path that activates it
// (ADR-0002 — the fuzzy classifier ships off, opt-in only). Every species that
// ships ENABLED in the adr0002 table stays enabled throughout, so enabling
// ai-slop is strictly per-species (no global switch). Sprint 019 added a second
// disabled-by-default species (todo-expired, report-only), so the expected
// enabled state is read from the adr0002 table rather than hard-coding ai-slop as
// the sole disabled species.
func TestAISlopShipsDisabled(t *testing.T) {
	r := NewResolver("", NewRegistry())

	// 1. Default run: each species' EffectiveEnabled matches its adr0002 row
	// (ai-slop and todo-expired ship disabled; every other built-in is enabled).
	def, err := r.Resolve(config.Config{})
	if err != nil {
		t.Fatalf("Resolve default: %v", err)
	}
	for _, rv := range def {
		want := adr0002[rv.Manifest.Name].enabled
		if rv.EffectiveEnabled != want {
			t.Errorf("default run: %s EffectiveEnabled = %v, want %v (per adr0002)", rv.Manifest.Name, rv.EffectiveEnabled, want)
		}
	}

	// 2. Opt-in: ant.toml [species.ai-slop] enabled = true activates ai-slop, and
	// ONLY it — the other species keep their default enabled state (in particular
	// the other disabled-by-default species, todo-expired, stays disabled, proving
	// the override is strictly per-species).
	on := true
	enabled, err := r.Resolve(config.Config{Species: map[string]config.Species{
		"ai-slop": {Enabled: &on},
	}})
	if err != nil {
		t.Fatalf("Resolve with ai-slop enabled: %v", err)
	}
	for _, rv := range enabled {
		want := rv.Manifest.Name == "ai-slop" || adr0002[rv.Manifest.Name].enabled
		if rv.EffectiveEnabled != want {
			t.Errorf("with ai-slop opt-in: %s EffectiveEnabled = %v, want %v (only ai-slop flips on; other defaults unchanged)", rv.Manifest.Name, rv.EffectiveEnabled, want)
		}
	}
}

// TestTodoExpiredShipsDisabled is the Sprint 019 report-only species' disabled-by-
// default assertion against the REAL embedded manifest: on a default run
// todo-expired resolves DISABLED (so the colony excludes it — it never runs or
// writes), and an explicit ant.toml [species.todo-expired] enabled=true is the
// only path that activates it, mirroring ai-slop's opt-in. Enabling it leaves the
// other species' enabled state untouched (per-species, no global switch).
func TestTodoExpiredShipsDisabled(t *testing.T) {
	r := NewResolver("", NewRegistry())

	def, err := r.Resolve(config.Config{})
	if err != nil {
		t.Fatalf("Resolve default: %v", err)
	}
	var found bool
	for _, rv := range def {
		if rv.Manifest.Name != "todo-expired" {
			continue
		}
		found = true
		if rv.EffectiveEnabled {
			t.Errorf("default run: todo-expired EffectiveEnabled = true, want false (report-only, ships disabled)")
		}
	}
	if !found {
		t.Fatalf("todo-expired missing from the embedded tree")
	}

	on := true
	enabled, err := r.Resolve(config.Config{Species: map[string]config.Species{
		"todo-expired": {Enabled: &on},
	}})
	if err != nil {
		t.Fatalf("Resolve with todo-expired enabled: %v", err)
	}
	for _, rv := range enabled {
		want := rv.Manifest.Name == "todo-expired" || adr0002[rv.Manifest.Name].enabled
		if rv.EffectiveEnabled != want {
			t.Errorf("with todo-expired opt-in: %s EffectiveEnabled = %v, want %v (only todo-expired flips on)", rv.Manifest.Name, rv.EffectiveEnabled, want)
		}
	}
}

// TestEmbed_FSContainsManifests is a lower-level guard that the embed directive
// actually captured each species.toml, independent of the resolver.
func TestEmbed_FSContainsManifests(t *testing.T) {
	fsys := builtins.FS()
	for name := range adr0002 {
		manifestPath := name + "/" + ManifestFileName
		if _, err := fsys.Open(manifestPath); err != nil {
			t.Errorf("embedded FS missing %s: %v", manifestPath, err)
		}
	}
}

func keysOf(m map[string]Resolved) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
