package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
)

// TestScaffoldWritesParseableConfig asserts `ant init` writes a config that
// round-trips through the loader: the scaffolded file must be valid TOML the
// engine itself can read back, with its trust defaults intact (TECHSPEC §7).
func TestScaffoldWritesParseableConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ant.toml")

	written, err := Scaffold(path, false)
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if written == "" {
		t.Fatal("Scaffold should return the path written")
	}

	cfg, found, err := Load(path)
	if err != nil {
		t.Fatalf("scaffolded config must parse through Load: %v", err)
	}
	if !found {
		t.Fatal("scaffolded file should be found")
	}
	// Trust defaults from ADR 0002 survive the round-trip.
	if ui, ok := cfg.SpeciesConfig("unused-import"); !ok || ui.AutoApply == nil || !*ui.AutoApply {
		t.Errorf("scaffolded unused-import.auto_apply should be true, got %+v (ok=%v)", ui, ok)
	}
	if slop, ok := cfg.SpeciesConfig("ai-slop"); !ok || slop.Enabled == nil || *slop.Enabled {
		t.Errorf("scaffolded ai-slop.enabled should be false, got %+v (ok=%v)", slop, ok)
	}
	// The quoted n+1-query key parses (the bare form would be invalid TOML).
	if _, ok := cfg.SpeciesConfig("n+1-query"); !ok {
		t.Error("scaffolded species.\"n+1-query\" should parse and be reachable by name")
	}
}

// TestScaffoldRefusesWithoutForce asserts a second run without --force fails
// cleanly (operational, exit 2) and does NOT clobber the existing file.
func TestScaffoldRefusesWithoutForce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ant.toml")

	if _, err := Scaffold(path, false); err != nil {
		t.Fatalf("first Scaffold: %v", err)
	}
	// Mutate the file so we can detect a clobber.
	sentinel := []byte("# user edited this\n")
	if err := os.WriteFile(path, sentinel, 0o644); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}

	_, err := Scaffold(path, false)
	if err == nil {
		t.Fatal("second Scaffold without --force must fail")
	}
	if !errors.Is(err, ErrConfigExists) {
		t.Errorf("want ErrConfigExists, got %v", err)
	}
	if !errors.Is(err, engine.ErrOperational) {
		t.Errorf("refusal must classify as operational (exit 2), got %v", err)
	}
	// No clobber: the user's content is intact.
	got, _ := os.ReadFile(path)
	if string(got) != string(sentinel) {
		t.Errorf("file was clobbered without --force; content = %q", string(got))
	}
}

// TestScaffoldForceOverwrites asserts --force replaces an existing file with the
// fresh scaffold.
func TestScaffoldForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ant.toml")
	if err := os.WriteFile(path, []byte("# stale\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := Scaffold(path, true); err != nil {
		t.Fatalf("forced Scaffold: %v", err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("overwritten config must parse: %v", err)
	}
	if _, ok := cfg.SpeciesConfig("unused-import"); !ok {
		t.Error("forced scaffold should contain the full template")
	}
}

// TestEnsureAntIgnoredCreatesFile asserts that, when no .gitignore exists beside
// the config, EnsureAntIgnored creates one containing the .ant/ entry.
func TestEnsureAntIgnoredCreatesFile(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "ant.toml")

	added, gi, err := EnsureAntIgnored(cfg)
	if err != nil {
		t.Fatalf("EnsureAntIgnored: %v", err)
	}
	if !added {
		t.Error("added = false, want true (a fresh .gitignore should get the entry)")
	}
	body, err := os.ReadFile(gi)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !alreadyIgnoresAnt(string(body)) {
		t.Errorf(".gitignore does not ignore .ant/ after EnsureAntIgnored:\n%s", body)
	}
}

// TestEnsureAntIgnoredIsIdempotent asserts a second call (and an existing entry)
// is a no-op that neither duplicates the line nor reports added.
func TestEnsureAntIgnoredIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "ant.toml")
	gi := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gi, []byte("node_modules/\n.ant/\n"), 0o644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}

	added, _, err := EnsureAntIgnored(cfg)
	if err != nil {
		t.Fatalf("EnsureAntIgnored: %v", err)
	}
	if added {
		t.Error("added = true, want false (.ant/ already ignored)")
	}
	body, _ := os.ReadFile(gi)
	if got, want := string(body), "node_modules/\n.ant/\n"; got != want {
		t.Errorf(".gitignore changed on idempotent call:\n got %q\nwant %q", got, want)
	}
}

// TestEnsureAntIgnoredAppendsPreservingContent asserts an existing .gitignore
// without the entry gets it appended, keeping prior lines and a clean newline
// boundary (no glued-together lines).
func TestEnsureAntIgnoredAppendsPreservingContent(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "ant.toml")
	gi := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gi, []byte("vendor/"), 0o644); err != nil { // no trailing newline
		t.Fatalf("seed .gitignore: %v", err)
	}

	added, _, err := EnsureAntIgnored(cfg)
	if err != nil {
		t.Fatalf("EnsureAntIgnored: %v", err)
	}
	if !added {
		t.Error("added = false, want true")
	}
	body, _ := os.ReadFile(gi)
	if got, want := string(body), "vendor/\n.ant/\n"; got != want {
		t.Errorf("append did not preserve content/newline:\n got %q\nwant %q", got, want)
	}
}
