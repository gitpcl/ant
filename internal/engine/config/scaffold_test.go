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
