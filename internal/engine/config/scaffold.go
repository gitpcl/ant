package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gitpcl/ant/internal/engine"
)

// DefaultConfigName is the conventional ant.toml filename in a project root.
const DefaultConfigName = "ant.toml"

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
