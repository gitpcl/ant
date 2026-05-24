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
// Which kinds exist is delegated to the registry (the single kind authority);
// what each kind requires (fields, referenced files) is owned here. File-
// existence checks run against fsys so the embedded and on-disk paths share one
// validation.
func validate(fsys fs.FS, dir string, m Manifest, reg *Registry) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s: %s", ErrInvalidManifest, manifestLabel(dir, m),
			fmt.Sprintf(format, args...))
	}

	// --- metadata ---
	if m.Name == "" {
		return bad("name is required")
	}
	if m.Severity == "" {
		return bad("severity is required")
	}
	if _, err := engine.ParseSeverity(m.Severity); err != nil {
		return bad("invalid severity %q (want low|medium|high)", m.Severity)
	}

	// --- [detector] ---
	// The registry decides whether the kind exists; an unknown kind surfaces as
	// "unknown [detector].kind". The per-kind required fields/files are checked
	// here once the kind is known to be valid.
	if m.Detector.Kind == "" {
		return bad("[detector].kind is required")
	}
	if !reg.KnownDetectorKind(m.Detector.Kind) {
		return bad("unknown [detector].kind %q (known: %s)", m.Detector.Kind, reg.DetectorKinds())
	}
	switch m.Detector.Kind {
	case DetectKindASTGrep:
		if m.Detector.Rule == "" {
			return bad("[detector].rule is required for kind=%q", DetectKindASTGrep)
		}
		if err := mustExist(fsys, dir, m.Detector.Rule); err != nil {
			return bad("[detector].rule %q: %v", m.Detector.Rule, err)
		}
	case DetectKindCommand:
		if m.Detector.Script == "" {
			return bad("[detector].script is required for kind=%q", DetectKindCommand)
		}
		if err := mustExist(fsys, dir, m.Detector.Script); err != nil {
			return bad("[detector].script %q: %v", m.Detector.Script, err)
		}
	}

	// --- [fix] ---
	if m.Fix.Kind == "" {
		return bad("[fix].kind is required")
	}
	if err := reg.CheckFixKind(m.Fix.Kind); err != nil {
		// Reframe the registry's unknown-kind error in manifest terms while
		// preserving ErrInvalidManifest classification.
		return bad("unknown [fix].kind %q (known: %s)", m.Fix.Kind, reg.FixKinds())
	}
	switch m.Fix.Kind {
	case FixKindLLM:
		// An llm fix WITHOUT a prompt is an error (TECHSPEC §6.2).
		if m.Fix.Prompt == "" {
			return bad("[fix].prompt is required for kind=%q", FixKindLLM)
		}
		if err := mustExist(fsys, dir, m.Fix.Prompt); err != nil {
			return bad("[fix].prompt %q: %v", m.Fix.Prompt, err)
		}
	case FixKindDeterministic:
		// A deterministic fix WITHOUT a prompt is VALID (TECHSPEC §6.2). If a
		// prompt file is named anyway, it must still exist.
		if m.Fix.Prompt != "" {
			if err := mustExist(fsys, dir, m.Fix.Prompt); err != nil {
				return bad("[fix].prompt %q: %v", m.Fix.Prompt, err)
			}
		}
	}

	// --- [verify] ---
	if len(m.Verify.Checks) == 0 {
		return bad("[verify].checks must list at least one check")
	}
	for _, check := range m.Verify.Checks {
		if err := reg.CheckVerifyKind(check); err != nil {
			return bad("unknown [verify] check %q (known: %s, or command:<script>)", check, reg.VerifyKinds())
		}
	}

	return nil
}

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
