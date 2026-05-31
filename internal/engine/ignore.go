package engine

import (
	"path/filepath"
	"strings"
)

// FilterIgnored returns the findings whose File is NOT excluded by any of the
// ignore globs (ant.toml's [ignore].paths, carried on Scope.IgnoreGlobs). It is
// the single scope/detector boundary that honors [ignore].paths: both front
// doors (scout's read-only fan-out and the colony's fix fan-out) call it on the
// merged finding set AFTER detection, so every detector inherits the same
// exclusion without a per-detector special case (TECHSPEC §8/§9). With no globs
// it returns the findings unchanged (zero-config scans the whole scope). The
// result is always a new slice — the input is never mutated (coding-style:
// immutability).
func FilterIgnored(findings []Finding, ignoreGlobs []string) []Finding {
	if len(ignoreGlobs) == 0 {
		out := make([]Finding, len(findings))
		copy(out, findings)
		return out
	}
	out := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if !PathIgnored(f.File, ignoreGlobs) {
			out = append(out, f)
		}
	}
	return out
}

// PathIgnored reports whether file matches any of the ignore globs. It accepts
// the forms the scaffolded ant.toml documents ([ignore].paths examples
// "vendor/", "node_modules/", "*_generated.go"):
//
//   - A trailing-slash entry ("vendor/") is a directory prefix: any file at or
//     under that directory is ignored.
//   - A slash-free entry ("*_generated.go") matches the file's BASENAME anywhere
//     in the tree, so a generated-file pattern need not name a directory.
//   - Any other entry is matched as a path glob (filepath.Match) against both the
//     full path and a directory-prefix interpretation, so "build/*" or an exact
//     relative path also work.
//
// Paths are normalized to forward slashes so behavior is identical across
// platforms and matches the forward-slash globs config carries.
func PathIgnored(file string, ignoreGlobs []string) bool {
	clean := normalizeSlash(file)
	base := pathBase(clean)
	for _, raw := range ignoreGlobs {
		g := normalizeSlash(strings.TrimSpace(raw))
		if g == "" {
			continue
		}
		if strings.HasSuffix(g, "/") {
			// Directory prefix: ignore the dir itself and everything under it.
			dir := strings.TrimSuffix(g, "/")
			if clean == dir || strings.HasPrefix(clean, dir+"/") {
				return true
			}
			continue
		}
		if !strings.Contains(g, "/") {
			// Basename glob: match the file name anywhere in the tree.
			if ok, _ := filepath.Match(g, base); ok {
				return true
			}
			continue
		}
		// Path glob: match the full relative path, and also treat the glob as a
		// directory prefix so "build" (no slash semantics) or "pkg/gen" ignores
		// files beneath it.
		if ok, _ := filepath.Match(g, clean); ok {
			return true
		}
		if clean == g || strings.HasPrefix(clean, g+"/") {
			return true
		}
	}
	return false
}

// normalizeSlash converts OS path separators to forward slashes and trims a
// leading "./" so a finding's File compares cleanly against config globs.
func normalizeSlash(p string) string {
	p = filepath.ToSlash(p)
	return strings.TrimPrefix(p, "./")
}

// pathBase returns the final path element of a forward-slash path.
func pathBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
