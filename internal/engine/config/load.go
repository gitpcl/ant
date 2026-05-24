package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/gitpcl/ant/internal/engine"
	toml "github.com/pelletier/go-toml/v2"
)

// Load reads and decodes an ant.toml file at path. A missing file is NOT an
// error — bare `ant` must work zero-config — so it returns the zero Config with
// found=false. A present-but-malformed file is an operational error (exit 2),
// wrapping engine.ErrOperational so the CLI classifies it without importing this
// package's internals. Unknown keys do not fail the load; they are collected as
// warnings (see LoadStrict) so a typo is surfaced loudly rather than silently
// ignored (TECHSPEC §9 acceptance: "unknown keys produce a clear warning").
func Load(path string) (cfg Config, found bool, err error) {
	cfg, _, found, err = LoadStrict(path)
	return cfg, found, err
}

// LoadStrict is Load plus the list of unknown-key warnings discovered during
// decode. The CLI renders these warnings to stderr; the engine never silently
// drops an unrecognized key. The warnings are human-readable, sorted for stable
// output, and each names the offending key path (e.g. "colony.concurency").
func LoadStrict(path string) (cfg Config, warnings []string, found bool, err error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil, false, nil
	}
	if err != nil {
		return Config{}, nil, false, fmt.Errorf("%w: read ant.toml %q: %v", engine.ErrOperational, path, err)
	}
	defer f.Close()

	cfg, warnings, err = decode(f, path)
	if err != nil {
		return Config{}, nil, true, err
	}
	return cfg, warnings, true, nil
}

// decode parses TOML from r into a Config. It runs the decoder in strict mode so
// any key not present in the schema is reported. go-toml/v2 returns a
// *toml.StrictMissingError listing every unknown key in one pass; we convert
// those to warnings and re-decode non-strictly so the known keys still load.
// True syntax errors (not unknown-key errors) are operational failures (exit 2).
func decode(r io.Reader, path string) (Config, []string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Config{}, nil, fmt.Errorf("%w: read ant.toml %q: %v", engine.ErrOperational, path, err)
	}

	var cfg Config
	strictErr := toml.NewDecoder(strings.NewReader(string(data))).DisallowUnknownFields().Decode(&cfg)
	if strictErr == nil {
		return cfg, nil, nil
	}

	var missing *toml.StrictMissingError
	if errors.As(strictErr, &missing) {
		// Unknown keys are warnings, not failures. Re-decode without the strict
		// flag so every recognized key still populates the Config; collect the
		// unknown-key paths for the caller to surface.
		var relaxed Config
		if err := toml.Unmarshal(data, &relaxed); err != nil {
			return Config{}, nil, fmt.Errorf("%w: parse ant.toml %q: %v", engine.ErrOperational, path, err)
		}
		return relaxed, unknownKeyWarnings(missing), nil
	}

	// A genuine syntax/type error: operational (exit 2).
	return Config{}, nil, fmt.Errorf("%w: parse ant.toml %q: %v", engine.ErrOperational, path, strictErr)
}

// unknownKeyWarnings turns a strict-decode miss into sorted, human-readable
// warning lines, one per unrecognized key path. Sorting keeps CLI output and
// any test assertions stable regardless of map/AST ordering.
func unknownKeyWarnings(missing *toml.StrictMissingError) []string {
	out := make([]string, 0, len(missing.Errors))
	for _, e := range missing.Errors {
		key := strings.Join(e.Key(), ".")
		out = append(out, fmt.Sprintf("unknown key %q in ant.toml (ignored)", key))
	}
	sort.Strings(out)
	return out
}
