package species

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/gitpcl/ant/internal/engine"
	toml "github.com/pelletier/go-toml/v2"
)

// ManifestFileName is the fixed file every species folder must contain
// (TECHSPEC §6.1).
const ManifestFileName = "species.toml"

// ErrInvalidManifest is the sentinel every manifest validation failure wraps so
// callers can classify a malformed species.toml with errors.Is regardless of
// the concrete message. A bad manifest is a configuration error, not a crash.
var ErrInvalidManifest = errors.New("species: invalid manifest")

// Load reads and validates the species.toml at dir within fsys, returning a
// typed Manifest. The same loader serves both the embedded built-in tree
// (embed.FS) and the on-disk user tree (os.DirFS over .ant/species) — fsys is
// the only difference, so built-in and user species go through identical
// parsing and validation (TECHSPEC §6.1, §6.3).
//
// dir is the species folder within fsys (e.g. "unused-import"); source is the
// human-readable provenance recorded on the Manifest (e.g.
// "embed:species/unused-import" or an absolute on-disk path). Referenced files
// (rule, prompt, script) are checked for existence relative to dir within the
// same fsys, so a manifest that names a missing detect.yml is rejected here, not
// at run time.
//
// reg is the kind authority: the loader delegates "is this kind known" to the
// registry (the single dispatch point) and owns only the per-kind structural
// rules (required fields, referenced-file existence). A nil reg falls back to
// the default registry so callers that only want to parse can pass nil.
func Load(fsys fs.FS, dir, source string, reg *Registry) (Manifest, error) {
	if reg == nil {
		reg = NewRegistry()
	}
	manifestPath := path.Join(dir, ManifestFileName)
	raw, err := fs.ReadFile(fsys, manifestPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: cannot read %s: %v", ErrInvalidManifest, manifestPath, err)
	}

	var m Manifest
	// Strict decode: unlike ant.toml (where unknown keys are warnings so a typo
	// does not break a zero-config run), a species.toml is an authored artifact
	// — an unknown key is a malformed manifest and is rejected here with
	// go-toml's precise location naming the offending key/line.
	if err := toml.NewDecoder(strings.NewReader(string(raw))).DisallowUnknownFields().Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("%w: %s: %v", ErrInvalidManifest, manifestPath, err)
	}

	// Collapse the [detect] alias into the canonical [detector] section so the
	// rest of the package only reads Detector. A manifest may use either
	// spelling, but not contradict itself by setting both.
	if (m.Detect != Detect{}) {
		if (m.Detector != Detect{}) {
			return Manifest{}, fmt.Errorf("%w: %s: set either [detector] or [detect], not both", ErrInvalidManifest, dir)
		}
		m.Detector = m.Detect
		m.Detect = Detect{}
	}

	m.Source = source

	if err := validate(fsys, dir, m, reg); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// validate enforces the TECHSPEC §6.2 manifest rules. Each failure names the
// specific problem and wraps ErrInvalidManifest so the caller can classify it.
// It is the fail-soft front for Load: it collects EVERY rule violation (via
// collectViolations) and joins them into one ErrInvalidManifest-wrapped error so
// a caller that only wants "is this manifest valid" still sees all problems at
// once. `ant species validate` reads the same per-violation list directly
// (collectViolations) to render each error on its own line.
func validate(fsys fs.FS, dir string, m Manifest, reg *Registry) error {
	violations := collectViolations(fsys, dir, m, reg)
	if len(violations) == 0 {
		return nil
	}
	// Join every violation under a single ErrInvalidManifest wrapper. errors.Is
	// still matches ErrInvalidManifest, and the joined message contains each
	// problem string, so the existing single-problem assertions keep passing while
	// a multi-error manifest now surfaces all of them.
	return fmt.Errorf("%w: %s", ErrInvalidManifest, strings.Join(violations, "; "))
}

// collectViolations runs every TECHSPEC §6.2 manifest rule and returns ONE
// message per problem (empty when the manifest is valid) — it never short-
// circuits on the first failure. The rule logic is owned here once; both
// validate (which joins these into a single error for Load) and Validate (which
// renders each for `ant species validate`) consume it, so the loader and the
// authoring command can never drift.
//
// Which kinds exist is delegated to the registry (the single kind authority);
// what each kind requires (fields, referenced files) is owned here. File-
// existence checks run against fsys so the embedded and on-disk paths share one
// validation. Where a downstream rule cannot run because an upstream field is
// absent or unknown (a missing [detector].kind makes a per-kind file check
// meaningless), that branch is skipped — the missing/unknown field is already
// reported, and a per-kind check on an absent kind would be noise.
func collectViolations(fsys fs.FS, dir string, m Manifest, reg *Registry) []string {
	label := manifestLabel(dir, m)
	var violations []string
	bad := func(format string, args ...any) {
		violations = append(violations, fmt.Sprintf("%s: %s", label, fmt.Sprintf(format, args...)))
	}

	// --- metadata ---
	if m.Name == "" {
		bad("name is required")
	}
	if m.Severity == "" {
		bad("severity is required")
	} else if _, err := engine.ParseSeverity(m.Severity); err != nil {
		bad("invalid severity %q (want low|medium|high)", m.Severity)
	}

	// --- [detector] ---
	// The registry decides whether the kind exists; an unknown kind surfaces as
	// "unknown [detector].kind". The per-kind required fields/files are checked
	// only once the kind is known valid (an unknown/absent kind makes the per-kind
	// file rule meaningless, so it is skipped — the kind error stands).
	switch {
	case m.Detector.Kind == "":
		bad("[detector].kind is required")
	case !reg.KnownDetectorKind(m.Detector.Kind):
		bad("unknown [detector].kind %q (known: %s)", m.Detector.Kind, reg.DetectorKinds())
	default:
		switch m.Detector.Kind {
		case DetectKindASTGrep:
			if m.Detector.Rule == "" {
				bad("[detector].rule is required for kind=%q", DetectKindASTGrep)
			} else if err := mustExist(fsys, dir, m.Detector.Rule); err != nil {
				bad("[detector].rule %q: %v", m.Detector.Rule, err)
			}
		case DetectKindCommand:
			if m.Detector.Script == "" {
				bad("[detector].script is required for kind=%q", DetectKindCommand)
			} else if err := mustExist(fsys, dir, m.Detector.Script); err != nil {
				bad("[detector].script %q: %v", m.Detector.Script, err)
			}
		}
	}

	// --- [fix] + [verify] ---
	violations = append(violations, collectFixViolations(fsys, dir, label, m, reg)...)

	return violations
}

// collectFixViolations runs the [fix] + [verify] rules, returning one message
// per problem. Split out of collectViolations to keep each function small; it
// shares the "report every problem" contract (no short-circuit). It returns
// early only when a kind is absent/unknown, because the per-kind and verify
// rules below cannot be meaningfully evaluated without a known fix kind.
func collectFixViolations(fsys fs.FS, dir, label string, m Manifest, reg *Registry) []string {
	var violations []string
	bad := func(format string, args ...any) {
		violations = append(violations, fmt.Sprintf("%s: %s", label, fmt.Sprintf(format, args...)))
	}

	if m.Fix.Kind == "" {
		bad("[fix].kind is required")
		return violations
	}
	if err := reg.CheckFixKind(m.Fix.Kind); err != nil {
		// Reframe the registry's unknown-kind error in manifest terms while
		// preserving ErrInvalidManifest classification.
		bad("unknown [fix].kind %q (known: %s)", m.Fix.Kind, reg.FixKinds())
		return violations
	}
	switch m.Fix.Kind {
	case FixKindLLM:
		// An llm fix WITHOUT a prompt is an error (TECHSPEC §6.2).
		if m.Fix.Prompt == "" {
			bad("[fix].prompt is required for kind=%q", FixKindLLM)
		} else if err := mustExist(fsys, dir, m.Fix.Prompt); err != nil {
			bad("[fix].prompt %q: %v", m.Fix.Prompt, err)
		}
	case FixKindDeterministic:
		// A deterministic fix WITHOUT a prompt is VALID (TECHSPEC §6.2). If a
		// prompt file is named anyway, it must still exist.
		if m.Fix.Prompt != "" {
			if err := mustExist(fsys, dir, m.Fix.Prompt); err != nil {
				bad("[fix].prompt %q: %v", m.Fix.Prompt, err)
			}
		}
	case FixKindTool:
		// A tool fix MUST declare the command it execs; args/timeout are optional
		// (Sprint 017). The command is resolved from PATH at fix time, so it is not
		// checked for existence here — a missing tool is a clean per-ant skip, not a
		// manifest error (CI must not require the formatter to be installed).
		if m.Fix.Command == "" {
			bad("[fix].command is required for kind=%q", FixKindTool)
		}
	case FixKindNone:
		// A none fix is REPORT-ONLY (Sprint 022 Finding 4): it proposes no change,
		// so it declares NO transform/prompt/command — and needs NO [verify].checks
		// (the non-empty rule below is skipped). This replaces the fake deterministic
		// [fix] + detector-clears [verify] workaround todo-expired carried (Sprint 019
		// ENGINE-GAP #2). Anything beyond the kind is meaningless here; rather than
		// silently ignore a stray field, reject it so a report-only manifest stays
		// minimal and an author is told the field has no effect.
		if m.Fix.Transform != "" || m.Fix.Prompt != "" || m.Fix.Command != "" {
			bad("[fix] kind=%q is report-only and must declare no transform/prompt/command", FixKindNone)
		}
	}

	// --- [verify] ---
	// A report-only species (fix kind none) proposes no change, so there is nothing
	// to verify: it needs NO [verify].checks. Every other kind must list at least
	// one check (a fix without a verifier gate would land unguarded).
	if m.Fix.Kind == FixKindNone {
		if len(m.Verify.Checks) > 0 {
			bad("[verify].checks must be empty for report-only kind=%q (nothing is fixed, so nothing is verified)", FixKindNone)
		}
		return violations
	}
	if len(m.Verify.Checks) == 0 {
		bad("[verify].checks must list at least one check")
		return violations
	}
	for _, check := range m.Verify.Checks {
		if err := reg.CheckVerifyKind(check); err != nil {
			bad("unknown [verify] check %q (known: %s, or command:<script>)", check, reg.VerifyKinds())
			continue
		}
		// A command:<script> check names a script that MUST exist in the species
		// folder (mirroring the [detector].script existence rule). An empty suffix
		// (a bare "command:") is rejected by the registry above; here we confirm the
		// referenced file is present so a typo fails at load, not at run time.
		if script, ok := strings.CutPrefix(check, CommandVerifyPrefix); ok {
			if err := mustExist(fsys, dir, script); err != nil {
				bad("[verify] check %q script %q: %v", check, script, err)
			}
		}
	}

	return violations
}

// CommandVerifyPrefix is the escape-hatch token prefix for a command verifier in
// [verify].checks (e.g. "command:verify.sh"). Kept here so the loader's
// script-existence check and the registry's prefix recognition agree on one
// spelling; the verify package mirrors it as verify.CommandCheckPrefix for the
// runtime adapter (the two packages do not import each other).
const CommandVerifyPrefix = "command:"

// mustExist reports an error if ref does not name a readable file within dir of
// fsys. Used to enforce "referenced files must exist per kind" (TECHSPEC §6.2).
func mustExist(fsys fs.FS, dir, ref string) error {
	full := path.Join(dir, ref)
	info, err := fs.Stat(fsys, full)
	if err != nil {
		return fmt.Errorf("file not found")
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory, not a file")
	}
	return nil
}

// manifestLabel produces a stable identifier for error messages, preferring the
// species name and falling back to the folder when name is unset.
func manifestLabel(dir string, m Manifest) string {
	if m.Name != "" {
		return m.Name
	}
	return dir
}
