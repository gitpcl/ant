package config

import "github.com/spf13/viper"

// ManifestDefaults carries the species-manifest layer of the resolution chain:
// the author-suggested values that sit ABOVE the built-in defaults but BELOW
// ant.toml and flags (TECHSPEC §9 order: flags > ant.toml > manifest > default).
// Only set fields participate; the colony-level manifest layer is typically
// empty (manifests describe species, not the colony), but the type leaves room
// for it and keeps the precedence chain explicit and testable.
type ManifestDefaults struct {
	Concurrency *int
	Fixer       *string
	Model       *string
}

// Resolver answers "what is the effective value of X for this run" with viper as
// the single precedence authority (TECHSPEC §9). It does not re-implement
// layering: it seeds viper's one default layer with built-in defaults overlaid
// by the species manifest (manifest wins over default), lets the ant.toml layer
// and the bound-flag layer sit on top in viper's native order, and reads
// effective values back with viper's getters.
//
// viper's native precedence is: explicit Set > flags > env > config-file >
// key/value store > defaults. We use only three of those bands — flags (bound
// pflags), config-file (ant.toml), and defaults — and collapse "manifest" into
// the defaults band by computing default-then-manifest before SetDefault, so the
// effective default already reflects the manifest override. This keeps the full
// four-level order (flags > toml > manifest > built-in) with viper owning every
// comparison.
type Resolver struct {
	v *viper.Viper
}

// NewResolver builds a Resolver over a viper instance whose flags are already
// bound (the CLI binds pflags before calling) and whose ant.toml has already
// been read into the config-file layer. It seeds the defaults layer from the
// built-in defaults overlaid by the manifest so the resolved value honors
// flags > toml > manifest > default. The same viper instance the CLI built for
// flag/file layering is reused — precedence is never recomputed elsewhere.
func NewResolver(v *viper.Viper, manifest ManifestDefaults) *Resolver {
	// Built-in defaults (lowest band), then overlay the manifest on top of them.
	// SetDefault is viper's lowest-priority layer, so flags and the config file
	// still win; computing manifest-over-default here gives the manifest its
	// correct slot between the file and the built-in default.
	concurrency := DefaultConcurrency()
	if manifest.Concurrency != nil {
		concurrency = *manifest.Concurrency
	}
	fixer := DefaultFixer
	if manifest.Fixer != nil {
		fixer = *manifest.Fixer
	}
	model := DefaultModel
	if manifest.Model != nil {
		model = *manifest.Model
	}

	v.SetDefault(KeyConcurrency, concurrency)
	v.SetDefault(KeyFixer, fixer)
	v.SetDefault(KeyModel, model)
	v.SetDefault(KeyVerifyMaxChangedLines, DefaultMaxChangedLines)
	v.SetDefault(KeyVerifyMaxChangedFiles, DefaultMaxChangedFiles)

	return &Resolver{v: v}
}

// Concurrency returns the effective parallel-ant count after the full
// resolution chain. A bound --concurrency flag wins; then ant.toml's
// [colony].concurrency; then the manifest; then NumCPU. A flag value of 0 (the
// pflag default) is treated as unset so it does not clobber a configured value.
func (r *Resolver) Concurrency() int {
	if n := r.v.GetInt(KeyConcurrency); n > 0 {
		return n
	}
	return DefaultConcurrency()
}

// Fixer returns the effective fixer adapter: flag > ant.toml > manifest >
// built-in default.
func (r *Resolver) Fixer() string {
	return r.v.GetString(KeyFixer)
}

// Model returns the effective model id: flag > ant.toml > manifest > built-in
// default. The model is always a resolved config value, never hardcoded at the
// call site (TECHSPEC §2).
func (r *Resolver) Model() string {
	return r.v.GetString(KeyModel)
}

// IgnorePaths returns the effective ignore globs from ant.toml's [ignore].paths,
// or nil if none are configured. There is no built-in default ignore set — an
// absent [ignore] section means "ignore nothing" so zero-config scans the whole
// scope.
func (r *Resolver) IgnorePaths() []string {
	return r.v.GetStringSlice(KeyIgnorePaths)
}

// MaxChangedLines returns the effective diff-bounded line cap: flag/toml >
// built-in default (DefaultMaxChangedLines). A configured 0 means unbounded on
// the line dimension. The colony passes this into verify.NewDiffBounded so the
// gate's size limit is a resolved config value, not a hardcoded constant.
func (r *Resolver) MaxChangedLines() int {
	return r.v.GetInt(KeyVerifyMaxChangedLines)
}

// MaxChangedFiles returns the effective diff-bounded file cap: flag/toml >
// built-in default (DefaultMaxChangedFiles). A configured 0 means unbounded on
// the file dimension.
func (r *Resolver) MaxChangedFiles() int {
	return r.v.GetInt(KeyVerifyMaxChangedFiles)
}
