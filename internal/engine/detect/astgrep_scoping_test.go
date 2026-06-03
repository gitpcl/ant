package detect

import (
	"context"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
)

// captureArgs returns a runner that records the args ast-grep would be invoked
// with, so a test can assert the language scoping (--globs) without a live binary.
func captureArgs(got *[]string) commandRunner {
	return func(_ context.Context, _ string, args []string) ([]byte, error) {
		*got = append([]string(nil), args...)
		return []byte("[]"), nil // empty match array
	}
}

// TestASTGrepScopesToPHPLanguage proves a `languages = ["php"]` species scopes the
// scan to *.php via an ast-grep --globs include pattern, so ast-grep walks only
// PHP files and never descends into unrelated trees (a node_modules/ JS tree is
// excluded by the include glob). This is the languages-scoped-detection guard.
func TestASTGrepScopesToPHPLanguage(t *testing.T) {
	var args []string
	det := NewASTGrep("hardcoded-secret", "detect.yml",
		withRunner(captureArgs(&args)),
		WithLanguages([]string{"php"}),
	)
	if _, err := det.Detect(context.Background(), engine.Scope{Root: "/repo"}); err != nil {
		t.Fatalf("Detect: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--globs **/*.php") {
		t.Errorf("php-scoped scan must include a --globs **/*.php pattern; got args=%v", args)
	}
	// A PHP species must NOT scan JS/TS trees — no .js/.ts/.go include glob is added.
	for _, unwanted := range []string{"*.js", "*.ts", "*.go", "*.py"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("php-scoped scan should not include %q; got args=%v", unwanted, args)
		}
	}
}

// TestASTGrepScopesToTSLanguage proves a `languages = ["typescript"]` species
// scopes to *.ts and *.tsx (both registered TypeScript extensions).
func TestASTGrepScopesToTSLanguage(t *testing.T) {
	var args []string
	det := NewASTGrep("missing-await", "detect.yml",
		withRunner(captureArgs(&args)),
		WithLanguages([]string{"typescript"}),
	)
	if _, err := det.Detect(context.Background(), engine.Scope{Root: "/repo"}); err != nil {
		t.Fatalf("Detect: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--globs **/*.ts", "--globs **/*.tsx"} {
		if !strings.Contains(joined, want) {
			t.Errorf("ts-scoped scan must include %q; got args=%v", want, args)
		}
	}
}

// TestASTGrepNoLanguagesIsUnscoped proves a species with NO declared languages
// (e.g. a Go-era built-in) adds no --globs and scans everything — the prior
// behavior is unchanged (Go species behavior unchanged, per the acceptance test).
func TestASTGrepNoLanguagesIsUnscoped(t *testing.T) {
	var args []string
	det := NewASTGrep("unused-import", "detect.yml",
		withRunner(captureArgs(&args)),
		WithLanguages(nil),
	)
	if _, err := det.Detect(context.Background(), engine.Scope{Root: "/repo"}); err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if strings.Contains(strings.Join(args, " "), "--globs") {
		t.Errorf("a species with no declared languages must be unscoped (no --globs); got args=%v", args)
	}
}
