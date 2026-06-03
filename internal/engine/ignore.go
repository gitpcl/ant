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
//   - A "**/SEGMENT/**" entry (the default-ignore form, e.g. "**/vendor/**")
//     matches when SEGMENT appears as ANY path segment of the file. This is
//     segment-anchored, NOT a prefix: it ignores noise dirs (vendor,
//     node_modules, .git, testdata) wherever they NEST below the scan root, but
//     because the file path is already relative-to-root (FilterIgnored runs on
//     post-detection findings whose File is root-relative), scanning INTO such a
//     dir — `ant scout ./x/testdata/foo` — yields files with NO "testdata"
//     segment, so root-level findings are never wrongly suppressed.
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
		if seg, ok := segmentGlob(g); ok {
			// "**/SEGMENT/**": ignore when SEGMENT is any path segment of the file.
			if hasPathSegment(clean, seg) {
				return true
			}
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

// segmentGlob recognizes the "**/SEGMENT/**" default-ignore form and returns the
// bare SEGMENT. It accepts only a single, slash-free segment between the leading
// "**/" and trailing "/**" (e.g. "**/vendor/**" → "vendor"); anything else is not
// a segment glob and falls through to the existing matchers.
func segmentGlob(g string) (string, bool) {
	const prefix, suffix = "**/", "/**"
	if !strings.HasPrefix(g, prefix) || !strings.HasSuffix(g, suffix) {
		return "", false
	}
	seg := g[len(prefix) : len(g)-len(suffix)]
	if seg == "" || strings.Contains(seg, "/") {
		return "", false
	}
	return seg, true
}

// hasPathSegment reports whether seg is one of the slash-separated segments of
// the forward-slash path clean. Matching whole segments (not substrings) means
// "**/git/**" does not match a file literally named "digit.go".
func hasPathSegment(clean, seg string) bool {
	for _, part := range strings.Split(clean, "/") {
		if part == seg {
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
