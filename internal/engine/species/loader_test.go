package species

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
)

// fixtureFS roots an os.DirFS at the testdata species tree so the loader runs
// against on-disk fixtures the same way it runs against the embedded FS.
func fixtureFS(t *testing.T) (fs string) {
	t.Helper()
	return "testdata/species"
}

// TestLoad_ValidDeterministic is the approach-gate spike: load ONE sample
// species.toml into the model and assert every field parses before any registry
// or resolution code exists. A deterministic fix with no prompt is valid.
func TestLoad_ValidDeterministic(t *testing.T) {
	m, err := Load(os.DirFS(fixtureFS(t)), "valid-deterministic", "testdata:valid-deterministic", nil)
	if err != nil {
		t.Fatalf("Load valid deterministic manifest: %v", err)
	}

	if m.Name != "unused-import" {
		t.Errorf("Name = %q, want %q", m.Name, "unused-import")
	}
	if m.Severity != "low" {
		t.Errorf("Severity = %q, want %q", m.Severity, "low")
	}
	if sev, err := m.ParsedSeverity(); err != nil || sev != engine.SeverityLow {
		t.Errorf("ParsedSeverity = %v, %v; want %v, nil", sev, err, engine.SeverityLow)
	}
	if !m.EffectiveAutoApply() {
		t.Errorf("EffectiveAutoApply = false, want true (manifest auto_apply=true)")
	}
	if !m.IsEnabled() {
		t.Errorf("IsEnabled = false, want true (enabled unset defaults to true)")
	}
	if m.Detector.Kind != DetectKindASTGrep || m.Detector.Rule != "detect.yml" {
		t.Errorf("Detector = %+v, want kind=ast-grep rule=detect.yml", m.Detector)
	}
	if m.Fix.Kind != FixKindDeterministic || m.Fix.Transform != "delete-match" {
		t.Errorf("Fix = %+v, want kind=deterministic transform=delete-match", m.Fix)
	}
	if m.Fix.Prompt != "" {
		t.Errorf("Fix.Prompt = %q, want empty (deterministic fix needs no prompt)", m.Fix.Prompt)
	}
	if len(m.Verify.Checks) != 2 {
		t.Errorf("Verify.Checks = %v, want 2 entries", m.Verify.Checks)
	}
	if m.Source != "testdata:valid-deterministic" {
		t.Errorf("Source = %q, want the passed-in provenance", m.Source)
	}
}

// TestLoad_ValidLLM_DetectAlias loads a valid llm manifest that uses the [detect]
// alias and a prompt file; both the alias collapse and the prompt-required rule
// must hold.
func TestLoad_ValidLLM_DetectAlias(t *testing.T) {
	m, err := Load(os.DirFS(fixtureFS(t)), "valid-llm", "testdata:valid-llm", nil)
	if err != nil {
		t.Fatalf("Load valid llm manifest: %v", err)
	}
	if m.Detector.Kind != DetectKindASTGrep {
		t.Errorf("Detector.Kind = %q, want ast-grep (from [detect] alias)", m.Detector.Kind)
	}
	if (m.Detect != Detect{}) {
		t.Errorf("Detect alias should be collapsed into Detector and cleared, got %+v", m.Detect)
	}
	if m.Fix.Kind != FixKindLLM || m.Fix.Prompt != "fix.md" {
		t.Errorf("Fix = %+v, want kind=llm prompt=fix.md", m.Fix)
	}
	if m.EffectiveAutoApply() {
		t.Errorf("EffectiveAutoApply = true, want false (manifest auto_apply=false)")
	}
}

// TestLoad_ValidReportOnly loads a report-only species (fix.kind=none, Sprint
// 022 Finding 4) and asserts it validates with NO [fix].transform/prompt and NO
// [verify].checks — the loader must not require that boilerplate for a species
// that proposes no change. IsReportOnly must report true.
func TestLoad_ValidReportOnly(t *testing.T) {
	m, err := Load(os.DirFS(fixtureFS(t)), "valid-report-only", "testdata:valid-report-only", nil)
	if err != nil {
		t.Fatalf("Load valid report-only manifest: %v", err)
	}
	if m.Fix.Kind != FixKindNone {
		t.Errorf("Fix.Kind = %q, want %q", m.Fix.Kind, FixKindNone)
	}
	if !m.IsReportOnly() {
		t.Errorf("IsReportOnly() = false, want true for fix.kind=none")
	}
	if m.Fix.Transform != "" || m.Fix.Prompt != "" || m.Fix.Command != "" {
		t.Errorf("report-only Fix must carry no transform/prompt/command, got %+v", m.Fix)
	}
	if len(m.Verify.Checks) != 0 {
		t.Errorf("Verify.Checks = %v, want empty for a report-only species", m.Verify.Checks)
	}
}

// TestLoad_InferredCapabilities asserts the four capability fields default
// correctly from the detector/fix kinds when a manifest declares no explicit
// capability metadata (Sprint 022 Future-Proofing #3). Each fixture covers a
// different inference path so the rules are pinned independently of any
// hand-edited manifest value.
func TestLoad_InferredCapabilities(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		want Capabilities
	}{
		{
			// ast-grep detector + deterministic fix: needs ast-grep on PATH, no
			// exec/network/report-only.
			name: "ast-grep deterministic",
			dir:  "valid-deterministic",
			want: Capabilities{RequiresTool: DetectKindASTGrep},
		},
		{
			// ast-grep detector + llm fix: requires_network inferred from kind=llm;
			// requires_tool still ast-grep.
			name: "ast-grep llm",
			dir:  "valid-llm",
			want: Capabilities{RequiresNetwork: true, RequiresTool: DetectKindASTGrep},
		},
		{
			// fix.kind=none: report_only inferred true; ast-grep detector still needs
			// ast-grep on PATH.
			name: "report-only",
			dir:  "valid-report-only",
			want: Capabilities{ReportOnly: true, RequiresTool: DetectKindASTGrep},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Load(os.DirFS(fixtureFS(t)), tc.dir, "testdata:"+tc.dir, nil)
			if err != nil {
				t.Fatalf("Load(%s): %v", tc.dir, err)
			}
			if got := m.Capabilities(); got != tc.want {
				t.Errorf("Capabilities() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestLoad_ExplicitCapabilities asserts explicit manifest capability values
// parse and override the inferred defaults (Sprint 022 Future-Proofing #3). The
// fixture is a command detector (requires_exec inferred true, left unset) that
// declares requires_tool="jq" and requires_network=true — both values the
// loader cannot derive from the kinds.
func TestLoad_ExplicitCapabilities(t *testing.T) {
	m, err := Load(os.DirFS(fixtureFS(t)), "valid-capabilities", "testdata:valid-capabilities", nil)
	if err != nil {
		t.Fatalf("Load valid-capabilities manifest: %v", err)
	}

	// Raw parsed pointers: unset stays nil (infer), explicit values are decoded.
	if m.RequiresExec != nil {
		t.Errorf("RequiresExec = %v, want nil (unset → inferred)", *m.RequiresExec)
	}
	if m.RequiresTool == nil || *m.RequiresTool != "jq" {
		t.Errorf("RequiresTool = %v, want explicit \"jq\"", m.RequiresTool)
	}
	if m.RequiresNetwork == nil || *m.RequiresNetwork != true {
		t.Errorf("RequiresNetwork = %v, want explicit true", m.RequiresNetwork)
	}

	// Effective capabilities: requires_exec inferred from the command detector,
	// requires_tool/requires_network from the explicit overrides, report_only
	// false (deterministic fix).
	want := Capabilities{
		RequiresExec:    true,
		RequiresNetwork: true,
		RequiresTool:    "jq",
		ReportOnly:      false,
	}
	if got := m.Capabilities(); got != want {
		t.Errorf("Capabilities() = %+v, want %+v", got, want)
	}
}

// TestManifest_Capabilities_Inference unit-tests the inference rules directly on
// the model, independent of any fixture file, so the requires_exec (tool-fix and
// command-detector) and requires_tool (tool-fix command) paths are pinned.
func TestManifest_Capabilities_Inference(t *testing.T) {
	toolFix := Manifest{
		Detector: Detect{Kind: DetectKindASTGrep},
		Fix:      Fix{Kind: FixKindTool, Command: "gofmt"},
	}
	got := toolFix.Capabilities()
	want := Capabilities{RequiresExec: true, RequiresTool: "gofmt"}
	if got != want {
		t.Errorf("tool fix Capabilities() = %+v, want %+v", got, want)
	}

	// Explicit override wins over inference (force requires_network off).
	off := false
	llm := Manifest{
		Detector:        Detect{Kind: DetectKindASTGrep},
		Fix:             Fix{Kind: FixKindLLM, Prompt: "fix.md"},
		RequiresNetwork: &off,
	}
	if got := llm.Capabilities(); got.RequiresNetwork {
		t.Errorf("explicit requires_network=false ignored: %+v", got)
	}
}

// TestManifest_SchemaVersion pins the manifest schema baseline (Sprint 022
// Future-Proofing #4) and its inference. The literal "1" is asserted (not just
// == ManifestSchemaVersion) so an unintended bump of the constant fails here,
// forcing a deliberate update of the value + progress_log.md together.
func TestManifest_SchemaVersion(t *testing.T) {
	const wantBaseline = "1"
	if ManifestSchemaVersion != wantBaseline {
		t.Fatalf("ManifestSchemaVersion = %q, want baseline %q; bump only deliberately (progress_log.md)", ManifestSchemaVersion, wantBaseline)
	}
	// A manifest that omits schema_version (every species authored before the
	// field existed) infers the baseline, so it still loads at a known version.
	if got := (Manifest{}).EffectiveSchemaVersion(); got != wantBaseline {
		t.Errorf("unset schema_version inferred %q, want baseline %q", got, wantBaseline)
	}
	// An explicit value wins over the inferred baseline.
	if got := (Manifest{SchemaVersion: "2"}).EffectiveSchemaVersion(); got != "2" {
		t.Errorf("explicit schema_version not honored: got %q, want %q", got, "2")
	}
}

// TestLoad_Malformed asserts each malformed fixture is rejected, wraps
// ErrInvalidManifest, and the message names the specific problem.
func TestLoad_Malformed(t *testing.T) {
	cases := []struct {
		dir      string
		wantText string
	}{
		{"malformed-llm-no-prompt", "prompt is required"},
		{"malformed-missing-rule", "file not found"},
		{"malformed-unknown-kind", "unknown [detector].kind"},
		{"malformed-none-with-verify", "must be empty for report-only"},
	}
	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			_, err := Load(os.DirFS(fixtureFS(t)), tc.dir, "testdata:"+tc.dir, nil)
			if err == nil {
				t.Fatalf("Load(%s) = nil error, want rejection", tc.dir)
			}
			if !errors.Is(err, ErrInvalidManifest) {
				t.Errorf("error %v does not wrap ErrInvalidManifest", err)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("error %q does not name the problem %q", err.Error(), tc.wantText)
			}
		})
	}
}
