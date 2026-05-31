package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gitpcl/ant/internal/engine"
)

// DefaultConfigName is the conventional ant.toml filename in a project root.
const DefaultConfigName = "ant.toml"

// antIgnoreEntry is the .gitignore line that excludes Ant's runtime state
// directory. `ant fix` writes staged diffs and run metadata under .ant/ in the
// working tree (the trust store lives user-local, not here), so .ant/ is
// machine-local state that must never be committed.
const antIgnoreEntry = ".ant/"

// ErrConfigExists reports that Scaffold refused to overwrite an existing config
// because --force was not given. It wraps engine.ErrOperational so the CLI maps
// the refusal to exit code 2 (operational) without importing this package's
// internals — a clean, non-zero failure that does not clobber the file.
var ErrConfigExists = fmt.Errorf("%w: ant.toml already exists (use --force to overwrite)", engine.ErrOperational)

// scaffoldTemplate is the commented, parseable ant.toml `ant init` writes. It
// mirrors the TECHSPEC §9 schema with sensible defaults (NumCPU concurrency is
// expressed as a comment because the literal CPU count is host-specific; the
// commented value documents the knob without pinning it). Species names
// containing non-bare-key characters (n+1-query) are quoted so the file is valid
// TOML — and it round-trips through Load (asserted by the scaffold test). The
// trust defaults match ADR 0002.
const scaffoldTemplate = `# ant.toml — Ant colony configuration (TECHSPEC §9).
# Every section is optional; ant runs zero-config without this file. Values here
# override species-manifest defaults, and command-line flags override these.

[colony]
# Max parallel ants. Omit to use the host CPU count (NumCPU), the built-in default.
# concurrency = 6
fixer = "pi"             # default fixer adapter: pi | claudecode | codex | rawmodel | deterministic
model = "qwen2.5-coder"  # model id for LLM-assisted fixers (never hardcoded; configured here)

[ignore]
# Path globs excluded from every run.
paths = ["vendor/", "node_modules/", "*_generated.go"]

# Per-species overrides. auto_apply overrides the species' author-suggested
# default; enabled toggles a species on or off. Trust is per-species — there is
# no global "trust everything" switch (TECHSPEC §6.3, ADR 0002).
[species.unused-import]
auto_apply = true         # deterministic, gated by compile + detector-clears

[species.dead-code]
auto_apply = true         # deterministic, gated by compile + detector-clears

[species."n+1-query"]
auto_apply = false        # LLM-assisted; always staged for review by default

[species.ai-slop]
enabled = false           # fuzzy classifier; ships disabled, opt in here
`

// Scaffold writes a commented ant.toml at path. It refuses to overwrite an
// existing file unless force is true, returning ErrConfigExists (exit 2) and
// leaving the existing file untouched (no clobber). On success it returns the
// absolute path written. The written content parses back through Load — a fresh
// scaffold is always a valid config (TECHSPEC §7 `ant init`).
func Scaffold(path string, force bool) (string, error) {
	if path == "" {
		path = DefaultConfigName
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("%w: resolve %q: %v", engine.ErrOperational, path, err)
	}

	if !force {
		if _, statErr := os.Stat(abs); statErr == nil {
			return "", ErrConfigExists
		} else if !errors.Is(statErr, os.ErrNotExist) {
			// A stat error other than "not found" (e.g. permission) is operational.
			return "", fmt.Errorf("%w: check %q: %v", engine.ErrOperational, abs, statErr)
		}
	}

	if err := os.WriteFile(abs, []byte(scaffoldTemplate), 0o644); err != nil {
		return "", fmt.Errorf("%w: write %q: %v", engine.ErrOperational, abs, err)
	}
	return abs, nil
}

// EnsureAntIgnored makes sure the .gitignore beside the config file ignores
// Ant's .ant/ state directory, so a user who runs `ant init` does not
// accidentally commit machine-local run state. It is idempotent: if an entry
// already covers .ant/ it changes nothing and reports added=false. It creates
// .gitignore when absent, or appends a single line (preserving existing content
// and ensuring a trailing newline) otherwise. configPath locates the project
// dir (its parent); "" means the current directory. A read/write failure is
// operational (exit 2).
func EnsureAntIgnored(configPath string) (added bool, gitignorePath string, err error) {
	dir := "."
	if configPath != "" {
		abs, aerr := filepath.Abs(configPath)
		if aerr != nil {
			return false, "", fmt.Errorf("%w: resolve %q: %v", engine.ErrOperational, configPath, aerr)
		}
		dir = filepath.Dir(abs)
	}
	gitignorePath = filepath.Join(dir, ".gitignore")

	data, rerr := os.ReadFile(gitignorePath)
	if rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		return false, gitignorePath, fmt.Errorf("%w: read %q: %v", engine.ErrOperational, gitignorePath, rerr)
	}
	if alreadyIgnoresAnt(string(data)) {
		return false, gitignorePath, nil
	}

	// Append the entry, preserving existing content and a clean newline boundary.
	out := append([]byte(nil), data...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, antIgnoreEntry...)
	out = append(out, '\n')
	if werr := os.WriteFile(gitignorePath, out, 0o644); werr != nil {
		return false, gitignorePath, fmt.Errorf("%w: write %q: %v", engine.ErrOperational, gitignorePath, werr)
	}
	return true, gitignorePath, nil
}

// alreadyIgnoresAnt reports whether a .gitignore body already excludes the .ant/
// state dir. It matches the common spellings on a line of their own (ignoring
// surrounding whitespace), which covers what EnsureAntIgnored and hand edits
// write; it does not attempt full gitignore-pattern semantics.
func alreadyIgnoresAnt(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		switch strings.TrimSpace(line) {
		case ".ant/", ".ant", "/.ant/", "/.ant":
			return true
		}
	}
	return false
}
