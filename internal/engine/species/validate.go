package species

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	toml "github.com/pelletier/go-toml/v2"
)

// ValidationFormat selects how a ValidationReport is rendered, mirroring
// doctor.Format / explain.Format: Human is an aligned, author-friendly layout;
// JSON is the single-document contract CI / authoring tooling parse.
type ValidationFormat int

const (
	// ValidationFormatHuman is the default aligned rendering.
	ValidationFormatHuman ValidationFormat = iota
	// ValidationFormatJSON emits the ValidationReport as one indented JSON
	// document with a trailing newline, matching the repo's other one-shot
	// machine-readable outputs (doctor, explain, species list).
	ValidationFormatJSON
)

// RenderValidation writes the report to w in the chosen format. It returns any
// write/encode error; it does not decide the exit code (the caller reads
// Report.OK for that), keeping the rendering pure like doctor/explain Render.
func RenderValidation(w io.Writer, format ValidationFormat, report ValidationReport) error {
	if format == ValidationFormatJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return fmt.Errorf("species validate: encode report: %w", err)
		}
		return nil
	}
	return renderValidationHuman(w, report)
}

// renderValidationHuman prints the validated folder's context (path, name,
// inferred capabilities) followed by either an OK line or one line per error,
// using the same tabwriter layout doctor / `ant species list` use so the front
// door feels consistent.
func renderValidationHuman(w io.Writer, report ValidationReport) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "path:\t%s\n", report.Path)
	fmt.Fprintf(tw, "manifest:\t%s\n", report.Manifest)
	if report.Name != "" {
		fmt.Fprintf(tw, "name:\t%s\n", report.Name)
	}
	if report.Severity != "" {
		fmt.Fprintf(tw, "severity:\t%s\n", report.Severity)
	}
	if report.SchemaVersion != "" {
		fmt.Fprintf(tw, "schema-version:\t%s\n", report.SchemaVersion)
	}
	if report.Capabilities != nil {
		c := report.Capabilities
		fmt.Fprintf(tw, "report-only:\t%t\n", c.ReportOnly)
		fmt.Fprintf(tw, "requires-exec:\t%t\n", c.RequiresExec)
		fmt.Fprintf(tw, "requires-network:\t%t\n", c.RequiresNetwork)
		if c.RequiresTool != "" {
			fmt.Fprintf(tw, "requires-tool:\t%s\n", c.RequiresTool)
		}
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("species validate: flush report: %w", err)
	}
	if report.OK {
		fmt.Fprintf(w, "\nvalid: %s is a well-formed species\n", report.Path)
		return nil
	}
	fmt.Fprintf(w, "\ninvalid: %s has %s\n", report.Path, plural(len(report.Errors), "problem", "problems"))
	for _, e := range report.Errors {
		fmt.Fprintf(w, "  - %s\n", e)
	}
	return nil
}

// plural renders "<n> <singular>" or "<n> <plural>" for the human summary line.
// Local to the package's validate rendering; kept tiny rather than importing a
// shared helper for one call site.
func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, pluralForm)
}

// validate.go owns `ant species validate <path>`: the authoring front door that
// checks a LOCAL species folder before it is published or installed (Sprint 022
// missing feature). It reuses the existing loader rules verbatim — the manifest
// decode (Load's strict TOML parse), the §6.2 schema rules + referenced-file
// existence (collectViolations), and the capability inference (Capabilities) —
// so a folder that validates here is exactly a folder Load/Install would accept.
// It does NOT reimplement any check.
//
// Unlike Load (which returns on the first violation), validate.go reports ALL
// problems at once: a TOML decode error is one report, and a parseable manifest
// surfaces every §6.2 rule it violates together, so an author fixes the folder
// in one pass instead of one Load at a time. The package owns this aggregation
// (TECHSPEC §3); cmd/ant only calls Validate, renders the Report, and maps an
// invalid result to a non-zero exit.

// ValidationReport is the result of validating one local species folder. It is
// the --json contract for `ant species validate`, mirroring the one-shot
// single-document shape of doctor.Report and explain.Detail (not a bus stream).
//
// OK is the single field that drives the command's exit code: true → the folder
// is a well-formed species (exit 0); false → at least one Error is present (exit
// 2). Name/Severity/Capabilities are best-effort context for a HUMAN reader —
// they are populated from whatever the manifest decoded, even when invalid, so
// the report is useful while errors are being fixed; consumers must gate on OK,
// not on those fields.
type ValidationReport struct {
	Path          string        `json:"path"`                     // the folder validated (as given)
	Manifest      string        `json:"manifest"`                 // the species.toml path validated
	OK            bool          `json:"ok"`                       // true iff Errors is empty
	Name          string        `json:"name,omitempty"`           // manifest name (best-effort)
	Severity      string        `json:"severity,omitempty"`       // manifest severity (best-effort)
	SchemaVersion string        `json:"schema_version,omitempty"` // effective manifest schema version (explicit or inferred baseline)
	Capabilities  *Capabilities `json:"capabilities,omitempty"`   // inferred capabilities (only when the manifest parsed)
	Errors        []string      `json:"errors,omitempty"`         // every problem found (empty when OK)
}

// Validate checks the species folder at dir on the local filesystem and returns
// a ValidationReport listing EVERY problem (never just the first). reg is the
// kind authority; a nil reg falls back to the default registry, matching Load.
//
// It performs the same three checks `ant species validate` advertises, all by
// reusing existing engine logic — never reimplementing a schema check:
//   - manifest schema: the strict TOML decode (a parse error, an unknown key) is
//     reported as the sole error, since no field-level rule can run on a manifest
//     that did not decode; a parseable manifest is then run through
//     collectViolations for every §6.2 rule at once.
//   - rule file resolves: collectViolations' mustExist enforces that the
//     manifest-referenced detect rule / script / prompt / command: verifier exist
//     in the folder (the same on-disk existence check Load/Install apply).
//   - capability metadata: the inferred + explicit Capabilities are computed
//     (Capabilities()) and attached so an author sees what the species will be
//     reported as requiring.
//
// Validate never returns an error: a malformed folder is a normal result carried
// in the Report (OK=false, Errors populated). The CLI decides the exit code from
// Report.OK. The folder is read-only — no file is written and no manifest script
// is ever executed (decode + fs.Stat only, exactly like Install's no-exec load).
func Validate(dir string, reg *Registry) ValidationReport {
	if reg == nil {
		reg = NewRegistry()
	}

	manifestRel := filepath.Join(dir, ManifestFileName)
	report := ValidationReport{Path: dir, Manifest: manifestRel}

	// Root an FS at the folder's PARENT and address the folder by its base name,
	// so the existing FS-based loader rules (collectViolations + mustExist) apply
	// unchanged. An absolute or "." dir is normalized through filepath so a bare
	// `ant species validate` (dir=".") resolves the current directory.
	abs, err := filepath.Abs(dir)
	if err != nil {
		report.Errors = []string{fmt.Sprintf("%s: cannot resolve path: %v", dir, err)}
		return report
	}
	parent := filepath.Dir(abs)
	base := filepath.Base(abs)
	fsys := os.DirFS(parent)
	dirSlash := filepath.ToSlash(base)

	manifestPath := path.Join(dirSlash, ManifestFileName)
	raw, readErr := fs.ReadFile(fsys, manifestPath)
	if readErr != nil {
		report.Errors = []string{fmt.Sprintf("%s: cannot read %s (is this a species folder?): %v", dir, ManifestFileName, readErr)}
		return report
	}

	// Decode with the SAME strict decoder Load uses (DisallowUnknownFields), so an
	// unknown key here is the exact rejection an install/run would hit. A decode
	// failure is terminal for this report: no field rule can run on a manifest that
	// did not parse, so it is the single reported error.
	var m Manifest
	if decErr := toml.NewDecoder(strings.NewReader(string(raw))).DisallowUnknownFields().Decode(&m); decErr != nil {
		report.Errors = []string{fmt.Sprintf("%s: %s: %v", base, ManifestFileName, decErr)}
		return report
	}

	// Collapse the [detect] alias into [detector] exactly as Load does, including
	// the "not both" contradiction check, before running the field rules.
	if (m.Detect != Detect{}) {
		if (m.Detector != Detect{}) {
			report.Errors = []string{fmt.Sprintf("%s: set either [detector] or [detect], not both", base)}
			return report
		}
		m.Detector = m.Detect
		m.Detect = Detect{}
	}
	m.Source = "validate:" + abs

	// Best-effort human context (gate on OK, not these).
	report.Name = m.Name
	report.Severity = m.Severity
	// Effective schema version (explicit or inferred baseline) so an author and
	// any --json consumer can read which species.toml schema this folder targets.
	report.SchemaVersion = m.EffectiveSchemaVersion()

	// REUSE the loader's §6.2 rule set — schema + referenced-file existence — and
	// collect EVERY violation rather than stopping at the first.
	violations := collectViolations(fsys, dirSlash, m, reg)
	sort.Strings(violations) // deterministic order for --json / golden stability
	report.Errors = violations
	report.OK = len(violations) == 0

	// Capabilities are only meaningful once the manifest parsed; attach them even
	// when field rules failed so an author sees the inferred requirements while
	// fixing the folder.
	caps := m.Capabilities()
	report.Capabilities = &caps

	return report
}
