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
