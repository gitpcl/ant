package species

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// fixturePath roots a fixture species folder at the on-disk testdata tree.
// Validate takes a real OS path (it validates a LOCAL folder), so tests address
// the folders directly rather than through an fs.FS like the loader tests.
func fixturePath(dir string) string {
	return filepath.Join("testdata", "species", dir)
}

// TestValidate_Valid asserts a well-formed folder validates clean: OK is true,
// no errors, and the manifest context + inferred capabilities are populated for
// the human report.
func TestValidate_Valid(t *testing.T) {
	report := Validate(fixturePath("valid-deterministic"), nil)
	if !report.OK {
		t.Fatalf("Validate(valid-deterministic).OK = false, errors = %v", report.Errors)
	}
	if len(report.Errors) != 0 {
		t.Errorf("valid folder reported errors: %v", report.Errors)
	}
	if report.Name != "unused-import" {
		t.Errorf("Name = %q, want unused-import", report.Name)
	}
	// The fixture declares no schema_version, so the report must carry the
	// inferred baseline — proving existing (pre-field) manifests still resolve a
	// version and that the inference flows into the --json/human report.
	if report.SchemaVersion != ManifestSchemaVersion {
		t.Errorf("SchemaVersion = %q, want inferred baseline %q", report.SchemaVersion, ManifestSchemaVersion)
	}
	if report.Capabilities == nil {
		t.Fatal("Capabilities not attached for a valid manifest")
	}
	// An ast-grep detector infers a requires_tool of "ast-grep" (the loader's
	// inference rule), proving capability metadata flows into the report.
	if report.Capabilities.RequiresTool != DetectKindASTGrep {
		t.Errorf("RequiresTool = %q, want %q", report.Capabilities.RequiresTool, DetectKindASTGrep)
	}
}

// TestValidate_ReportOnly asserts a report-only folder (fix.kind=none) validates
// and surfaces ReportOnly=true in the capability metadata `ant species validate`
// reads.
func TestValidate_ReportOnly(t *testing.T) {
	report := Validate(fixturePath("valid-report-only"), nil)
	if !report.OK {
		t.Fatalf("Validate(valid-report-only).OK = false, errors = %v", report.Errors)
	}
	if report.Capabilities == nil || !report.Capabilities.ReportOnly {
		t.Errorf("report-only species not flagged report-only: %+v", report.Capabilities)
	}
}

// TestValidate_Invalid asserts a malformed folder is rejected (OK=false) and the
// reported error names the specific problem (the existing single-problem
// fixtures reused verbatim from the loader tests).
func TestValidate_Invalid(t *testing.T) {
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
			report := Validate(fixturePath(tc.dir), nil)
			if report.OK {
				t.Fatalf("Validate(%s).OK = true, want invalid", tc.dir)
			}
			if len(report.Errors) == 0 {
				t.Fatalf("Validate(%s) reported no errors", tc.dir)
			}
			if !containsAny(report.Errors, tc.wantText) {
				t.Errorf("errors %v do not name the problem %q", report.Errors, tc.wantText)
			}
		})
	}
}

// TestValidate_AggregatesAllErrors is the load-bearing assertion for this
// feature: a folder with SEVERAL independent §6.2 violations must report ALL of
// them in one pass, not stop at the first. The malformed-multi fixture carries
// an invalid severity, a missing detect rule, an llm fix with no prompt, and an
// unknown verify check.
func TestValidate_AggregatesAllErrors(t *testing.T) {
	report := Validate(fixturePath("malformed-multi"), nil)
	if report.OK {
		t.Fatal("malformed-multi validated OK, want invalid")
	}
	wantEach := []string{
		"invalid severity",   // bad metadata
		"file not found",     // missing detect.yml (rule resolution)
		"prompt is required", // llm fix without a prompt
		"unknown [verify]",   // bogus verify check
	}
	for _, want := range wantEach {
		if !containsAny(report.Errors, want) {
			t.Errorf("aggregated errors %v missing %q (validate must not stop at the first problem)", report.Errors, want)
		}
	}
	if len(report.Errors) < len(wantEach) {
		t.Errorf("got %d errors, want at least %d distinct problems: %v", len(report.Errors), len(wantEach), report.Errors)
	}
}

// TestValidate_NotASpeciesFolder asserts a folder with no species.toml is a
// clean invalid result (not a panic / not an error return) so the CLI renders a
// helpful message and exits non-zero.
func TestValidate_NotASpeciesFolder(t *testing.T) {
	report := Validate(t.TempDir(), nil)
	if report.OK {
		t.Fatal("empty dir validated OK, want invalid")
	}
	if !containsAny(report.Errors, "cannot read "+ManifestFileName) {
		t.Errorf("errors %v do not explain the missing manifest", report.Errors)
	}
}

// TestRenderValidation_JSONRoundTrip pins the --json contract: the rendered JSON
// decodes back into an equivalent ValidationReport. This guards the field shape
// (names, omitempty) external authoring tooling depends on against an accidental
// change (the repo convention for one-shot --json outputs).
func TestRenderValidation_JSONRoundTrip(t *testing.T) {
	report := Validate(fixturePath("valid-deterministic"), nil)

	var buf bytes.Buffer
	if err := RenderValidation(&buf, ValidationFormatJSON, report); err != nil {
		t.Fatalf("RenderValidation(JSON) error: %v", err)
	}

	var got ValidationReport
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("rendered JSON does not decode: %v\n%s", err, buf.String())
	}
	if got.OK != report.OK || got.Name != report.Name || got.Severity != report.Severity {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", got, report)
	}
	if got.Capabilities == nil || got.Capabilities.RequiresTool != report.Capabilities.RequiresTool {
		t.Errorf("capabilities lost in round-trip: got %+v want %+v", got.Capabilities, report.Capabilities)
	}
	// The top-level key set is part of the contract; assert the load-bearing ones
	// are present so a rename is caught by this test.
	for _, key := range []string{`"path"`, `"manifest"`, `"ok"`, `"capabilities"`} {
		if !strings.Contains(buf.String(), key) {
			t.Errorf("JSON missing contract key %s:\n%s", key, buf.String())
		}
	}
}

// TestRenderValidation_HumanInvalid asserts the human renderer lists every error
// under an "invalid" summary, so a terminal author sees all problems.
func TestRenderValidation_HumanInvalid(t *testing.T) {
	report := Validate(fixturePath("malformed-multi"), nil)
	var buf bytes.Buffer
	if err := RenderValidation(&buf, ValidationFormatHuman, report); err != nil {
		t.Fatalf("RenderValidation(Human) error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "invalid:") {
		t.Errorf("human output missing invalid summary:\n%s", out)
	}
	for _, e := range report.Errors {
		if !strings.Contains(out, e) {
			t.Errorf("human output omitted error %q:\n%s", e, out)
		}
	}
}

// containsAny reports whether any element of list contains substr.
func containsAny(list []string, substr string) bool {
	for _, s := range list {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}
