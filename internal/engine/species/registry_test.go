package species

import (
	"errors"
	"strings"
	"testing"
)

// TestRegistry_DetectorKnownKind resolves the ast-grep detector kind to a
// concrete, non-nil Detector through the registry — the single dispatch point.
func TestRegistry_DetectorKnownKind(t *testing.T) {
	reg := NewRegistry()
	det, err := reg.Detector(DetectKindASTGrep, "unused-import", "species/unused-import/detect.yml")
	if err != nil {
		t.Fatalf("Detector(ast-grep) = error %v, want a detector", err)
	}
	if det == nil {
		t.Fatal("Detector(ast-grep) = nil, want a concrete adapter")
	}
}

// TestRegistry_DetectorUnknownKind asserts an unknown detect kind fails loudly:
// it wraps ErrUnknownKind and names the bad kind in the message.
func TestRegistry_DetectorUnknownKind(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.Detector("semgrep-xyz", "x", "rule.yml")
	if err == nil {
		t.Fatal("Detector(unknown) = nil error, want loud failure")
	}
	if !errors.Is(err, ErrUnknownKind) {
		t.Errorf("error %v does not wrap ErrUnknownKind", err)
	}
	if !strings.Contains(err.Error(), "semgrep-xyz") {
		t.Errorf("error %q does not name the bad kind", err.Error())
	}
}

// TestRegistry_FixKinds asserts known fix kinds pass and an unknown one fails
// loudly naming the bad kind.
func TestRegistry_FixKinds(t *testing.T) {
	reg := NewRegistry()
	for _, k := range []string{FixKindDeterministic, FixKindLLM} {
		if err := reg.CheckFixKind(k); err != nil {
			t.Errorf("CheckFixKind(%q) = %v, want nil", k, err)
		}
	}
	err := reg.CheckFixKind("magic")
	if err == nil || !errors.Is(err, ErrUnknownKind) || !strings.Contains(err.Error(), "magic") {
		t.Errorf("CheckFixKind(magic) = %v, want ErrUnknownKind naming %q", err, "magic")
	}
}

// TestRegistry_VerifyKinds asserts built-in checks pass, the command: escape
// hatch passes by prefix, and an unknown check fails loudly naming the bad kind.
func TestRegistry_VerifyKinds(t *testing.T) {
	reg := NewRegistry()
	for _, k := range []string{"compile", "tests:affected", "tests:all", "detector-clears", "diff-bounded", "command:verify.sh"} {
		if err := reg.CheckVerifyKind(k); err != nil {
			t.Errorf("CheckVerifyKind(%q) = %v, want nil", k, err)
		}
	}
	err := reg.CheckVerifyKind("lint:everything")
	if err == nil || !errors.Is(err, ErrUnknownKind) || !strings.Contains(err.Error(), "lint:everything") {
		t.Errorf("CheckVerifyKind(lint:everything) = %v, want ErrUnknownKind naming the bad kind", err)
	}
	// A bare "command:" with no script is NOT a valid escape hatch.
	if err := reg.CheckVerifyKind("command:"); err == nil {
		t.Errorf("CheckVerifyKind(\"command:\") = nil, want rejection (no script named)")
	}
}
