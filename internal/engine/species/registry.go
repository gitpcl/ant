package species

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/detect"
)

// ErrUnknownKind is the sentinel every unknown-kind failure wraps. A manifest
// that names a detect/fix/verify kind the registry does not know fails LOUDLY
// (TECHSPEC §6.2) — errors.Is(err, ErrUnknownKind) classifies it without
// importing the concrete message.
var ErrUnknownKind = errors.New("species: unknown kind")

// DetectorConstructor builds a Detector for a species from its detect section.
// species is the owning species name (for finding provenance); ruleRef is the
// rule/script path already resolved to where the adapter can read it (the
// caller joins the species folder root). Returning the engine interface keeps
// the registry the single place that knows concrete adapter types.
type DetectorConstructor func(species, ruleRef string) (engine.Detector, error)

// Registry is the SINGLE dispatch point from a manifest's kind strings to
// concrete adapter constructors (TECHSPEC §6, sprint contract). It is an
// explicit constructor map — NOT reflection-based plugin loading, which was
// rejected as over-engineered for the fixed, small kind set. Adding a new kind
// is a one-line map entry, and an unknown kind fails loudly naming the bad kind.
//
// Fix and verify adapters land in later sprints (M2/M3); the registry already
// recognizes their valid kind tokens so a manifest validates against the closed
// set today, and reports a clear "not yet wired" error (still naming the kind)
// when a not-yet-built adapter is requested — distinct from the unknown-kind
// error so a typo and an unbuilt-but-valid kind are never conflated.
type Registry struct {
	detectors  map[string]DetectorConstructor
	fixKinds   map[string]struct{}
	verifyBase map[string]struct{}
}

// NewRegistry returns the default registry wired with the v1 adapter kinds
// (TECHSPEC §6.2, ADR-0002). Detect: ast-grep (concrete) and command (escape
// hatch, reserved). Fix: deterministic, llm. Verify: compile, tests:affected,
// tests:all, detector-clears, diff-bounded. The command:* verify escape hatch is
// recognized by prefix in VerifyKind so a manifest can name "command:verify.sh".
func NewRegistry() *Registry {
	return &Registry{
		detectors: map[string]DetectorConstructor{
			DetectKindASTGrep: func(species, ruleRef string) (engine.Detector, error) {
				return detect.NewASTGrep(species, ruleRef), nil
			},
			// command detector adapter is the script escape hatch (TECHSPEC §4
			// detect/command.go) — its kind is valid and reserved here, but the
			// adapter itself lands in a later sprint.
			DetectKindCommand: func(species, ruleRef string) (engine.Detector, error) {
				return nil, fmt.Errorf("species: detector kind %q is valid but not yet wired (script escape hatch lands in a later sprint)", DetectKindCommand)
			},
		},
		fixKinds: map[string]struct{}{
			FixKindDeterministic: {},
			FixKindLLM:           {},
		},
		verifyBase: map[string]struct{}{
			"compile":         {},
			"tests:affected":  {},
			"tests:all":       {},
			"detector-clears": {},
			"diff-bounded":    {},
		},
	}
}

// Detector resolves a detect kind to a concrete Detector via the named
// constructor. An unknown kind returns an error wrapping ErrUnknownKind and
// naming the bad kind plus the set of known kinds. A valid-but-unwired kind
// (e.g. command today) returns a distinct, non-ErrUnknownKind error so callers
// can tell a typo from a deferred adapter.
func (r *Registry) Detector(kind, species, ruleRef string) (engine.Detector, error) {
	ctor, ok := r.detectors[kind]
	if !ok {
		return nil, fmt.Errorf("%w: detector kind %q (known: %s)", ErrUnknownKind, kind, knownKeys(r.detectors))
	}
	return ctor(species, ruleRef)
}

// KnownDetectorKind reports whether a detect kind is registered.
func (r *Registry) KnownDetectorKind(kind string) bool {
	_, ok := r.detectors[kind]
	return ok
}

// DetectorKinds returns the sorted set of registered detect kinds for error
// messages.
func (r *Registry) DetectorKinds() string { return knownKeys(r.detectors) }

// FixKinds returns the sorted set of recognized fix kinds for error messages.
func (r *Registry) FixKinds() string { return knownSet(r.fixKinds) }

// VerifyKinds returns the sorted set of recognized built-in verify kinds for
// error messages (the command:<script> escape hatch is matched by prefix and
// noted separately by callers).
func (r *Registry) VerifyKinds() string { return knownSet(r.verifyBase) }

// KnownFixKind reports whether kind is a recognized fix kind. The concrete fix
// adapters (deterministic, rawmodel, harness execs) are built in the M2/M3
// fix-engine sprints; the registry's job this sprint is to validate the closed
// kind set and fail loudly on anything outside it.
func (r *Registry) KnownFixKind(kind string) bool {
	_, ok := r.fixKinds[kind]
	return ok
}

// CheckFixKind returns nil for a known fix kind and an ErrUnknownKind-wrapping
// error (naming the bad kind) otherwise — the loud-failure path the contract
// requires for the fix slot.
func (r *Registry) CheckFixKind(kind string) error {
	if r.KnownFixKind(kind) {
		return nil
	}
	return fmt.Errorf("%w: fix kind %q (known: %s)", ErrUnknownKind, kind, knownSet(r.fixKinds))
}

// KnownVerifyKind reports whether a verify check token is recognized. Built-in
// kinds match exactly; the script escape hatch matches the "command:" prefix
// (e.g. "command:verify.sh") per TECHSPEC §6.2.
func (r *Registry) KnownVerifyKind(kind string) bool {
	if _, ok := r.verifyBase[kind]; ok {
		return true
	}
	const escapeHatch = "command:"
	return len(kind) > len(escapeHatch) && kind[:len(escapeHatch)] == escapeHatch
}

// CheckVerifyKind returns nil for a known verify kind and an
// ErrUnknownKind-wrapping error (naming the bad kind) otherwise.
func (r *Registry) CheckVerifyKind(kind string) error {
	if r.KnownVerifyKind(kind) {
		return nil
	}
	return fmt.Errorf("%w: verify check %q (known: %s, or command:<script>)", ErrUnknownKind, kind, knownSet(r.verifyBase))
}

// knownKeys renders the sorted keys of a detector-constructor map for error
// messages, so an unknown-kind error tells the author exactly what is valid.
func knownKeys(m map[string]DetectorConstructor) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// knownSet renders the sorted keys of a string-set for error messages.
func knownSet(m map[string]struct{}) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
