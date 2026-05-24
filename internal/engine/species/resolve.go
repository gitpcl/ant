package species

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"

	"github.com/gitpcl/ant/internal/engine/config"
	builtins "github.com/gitpcl/ant/species"
)

// Origin records where a resolved species came from, for provenance and so the
// trust layer (Sprint 011) can treat installed species differently from
// built-ins (TECHSPEC §6.3).
type Origin int

const (
	// OriginBuiltin is a species embedded in the binary (TECHSPEC §4 tree).
	OriginBuiltin Origin = iota
	// OriginUser is a species discovered on disk under .ant/species/.
	OriginUser
)

// String renders an Origin as a stable lowercase token for output/logging.
func (o Origin) String() string {
	if o == OriginUser {
		return "user"
	}
	return "builtin"
}

// Resolved is a species after resolution: its validated manifest, where it came
// from, and the effective auto_apply computed from the ant.toml override layered
// over the manifest default (TECHSPEC §6.3). The freshly-installed propose-only
// override (TECHSPEC §6.3, second bullet) is DEFERRED to Sprint 011 (security)
// and is intentionally not applied here — resolution this sprint is config +
// manifest only.
type Resolved struct {
	Manifest           Manifest
	Origin             Origin
	EffectiveAutoApply bool
	EffectiveEnabled   bool
}

// Resolver discovers and merges species from the embedded built-in tree and the
// on-disk user tree, then layers the ant.toml overrides on top. It owns the
// shadowing rule (user species override same-named built-ins) and the effective
// auto_apply/enabled computation. The CLI calls Resolve once per run and reads
// the results; all the merge logic lives here, not in cmd/ant.
type Resolver struct {
	builtinFS fs.FS  // embedded built-in tree (root holds species folders)
	userRoot  string // on-disk .ant/species directory (may not exist)
	registry  *Registry
}

// NewResolver wires the resolver to the embedded built-in species tree and the
// on-disk user tree at userRoot (typically ".ant/species"). A missing userRoot
// is not an error — a project with no user species resolves to the built-ins
// alone (TECHSPEC §6.3, zero-config). reg is the kind authority used during
// load; nil falls back to the default registry.
func NewResolver(userRoot string, reg *Registry) *Resolver {
	if reg == nil {
		reg = NewRegistry()
	}
	bfs := builtins.FS()
	return &Resolver{builtinFS: bfs, userRoot: userRoot, registry: reg}
}

// newResolverFS is the test seam: it injects an arbitrary built-in FS instead of
// the real embed, so resolution can be exercised against fixtures without the
// embedded tree. Production code uses NewResolver.
func newResolverFS(builtinFS fs.FS, userRoot string, reg *Registry) *Resolver {
	if reg == nil {
		reg = NewRegistry()
	}
	return &Resolver{builtinFS: builtinFS, userRoot: userRoot, registry: reg}
}

// Resolve discovers every built-in and user species, applies the shadowing rule
// (user shadows built-in of the same name), layers the ant.toml override for
// effective auto_apply/enabled, and returns the merged set sorted by name. cfg
// supplies the per-species ant.toml overrides via cfg.SpeciesConfig(name).
//
// A malformed manifest in either tree is returned as an error (wrapping
// ErrInvalidManifest), naming the offending species — a broken species must not
// be silently dropped from the colony.
func (r *Resolver) Resolve(cfg config.Config) ([]Resolved, error) {
	// Built-ins first (lowest layer), then user species overwrite by name.
	merged := map[string]Resolved{}

	builtin, err := r.discover(r.builtinFS, "embed:species")
	if err != nil {
		return nil, err
	}
	for _, m := range builtin {
		merged[m.Name] = r.resolveOne(m, OriginBuiltin, cfg)
	}

	// User species: only if the directory exists. os.DirFS over a missing dir
	// would surface as a read error, so guard with a stat first.
	if r.userRoot != "" {
		if info, statErr := os.Stat(r.userRoot); statErr == nil && info.IsDir() {
			user, err := r.discover(os.DirFS(r.userRoot), r.userRoot)
			if err != nil {
				return nil, err
			}
			for _, m := range user {
				merged[m.Name] = r.resolveOne(m, OriginUser, cfg)
			}
		}
	}

	out := make([]Resolved, 0, len(merged))
	for _, v := range merged {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.Name < out[j].Manifest.Name })
	return out, nil
}

// discover loads every species folder directly under the root of fsys. A
// species folder is any directory containing a species.toml; directories
// without one are skipped (they are not species). sourcePrefix is prepended to
// each species' Source for provenance (e.g. "embed:species/unused-import").
func (r *Resolver) discover(fsys fs.FS, sourcePrefix string) ([]Manifest, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("species: cannot read tree %q: %w", sourcePrefix, err)
	}
	var out []Manifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := e.Name()
		// Skip directories without a manifest: not every subfolder is a species.
		if _, statErr := fs.Stat(fsys, path.Join(dir, ManifestFileName)); statErr != nil {
			continue
		}
		m, err := Load(fsys, dir, sourcePrefix+"/"+dir, r.registry)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// resolveOne computes the effective trust fields for a single manifest by
// layering the ant.toml override (cfg) over the manifest defaults:
//
//   - effective auto_apply = ant.toml [species.<name>].auto_apply if set, else
//     the manifest's value (TECHSPEC §6.3). No global trust switch exists.
//   - effective enabled    = ant.toml [species.<name>].enabled if set, else the
//     manifest's value (so ai-slop ships disabled and is enabled via ant.toml).
//
// The freshly-installed propose-only override is intentionally NOT applied here
// (deferred to Sprint 011).
func (r *Resolver) resolveOne(m Manifest, origin Origin, cfg config.Config) Resolved {
	autoApply := m.EffectiveAutoApply()
	enabled := m.IsEnabled()

	if sc, ok := cfg.SpeciesConfig(m.Name); ok {
		if sc.AutoApply != nil {
			autoApply = *sc.AutoApply
		}
		if sc.Enabled != nil {
			enabled = *sc.Enabled
		}
	}

	return Resolved{
		Manifest:           m,
		Origin:             origin,
		EffectiveAutoApply: autoApply,
		EffectiveEnabled:   enabled,
	}
}

// IsMalformed reports whether err is a manifest-validation failure (so callers
// can distinguish a bad species from an I/O error). It is a thin wrapper over
// errors.Is(err, ErrInvalidManifest) kept here so resolution callers do not need
// to import the loader sentinel directly.
func IsMalformed(err error) bool { return errors.Is(err, ErrInvalidManifest) }
