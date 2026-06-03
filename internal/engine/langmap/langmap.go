// Package langmap is the SINGLE file-extension → language authority for the
// engine. Both detector scoping (which file trees ast-grep should scan for a
// given species' languages) and verifier dispatch (which per-language compile
// builder / test selector a diff resolves to) consume it, so there is never a
// second, divergent extension map anywhere in the codebase.
//
// The map is intentionally small and table-driven: adding a language is one
// row here, and every consumer picks it up. An extension with no row resolves
// to Unknown — the honest-skip signal the verifiers turn into a visible
// "no checker for <lang>" rather than a vacuous green (Sprint 026).
package langmap

import (
	"path/filepath"
	"strings"
)

// Canonical language tokens. These are the keys the per-language verifier
// tables (compile BuildTable, tests:affected runner table) and the detector
// scoping use, so they are defined once here and referenced everywhere.
const (
	Go         = "go"
	PHP        = "php"
	Python     = "python"
	TypeScript = "typescript"
	JavaScript = "javascript"
	Vue        = "vue"
	// Unknown is the stable sentinel for any extension with no registered
	// language. Consumers treat it as "no language resolved" → honest skip,
	// never a silent pass.
	Unknown = "unknown"
)

// extToLanguage is the one authority. Keys are lowercase extensions WITH the
// leading dot, matching filepath.Ext output. Several extensions map to the same
// canonical language (.ts/.tsx → typescript, .js/.jsx → javascript).
var extToLanguage = map[string]string{
	".go":  Go,
	".php": PHP,
	".py":  Python,
	".ts":  TypeScript,
	".tsx": TypeScript,
	".js":  JavaScript,
	".jsx": JavaScript,
	".vue": Vue,
}

// LanguageForPath resolves a file path to its canonical language token by file
// extension, or Unknown if the extension is not registered. The lookup is
// case-insensitive on the extension (".PHP" resolves to php) so a mixed-case
// path on a case-insensitive filesystem still resolves. An empty or
// extension-less path is Unknown.
func LanguageForPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if lang, ok := extToLanguage[ext]; ok {
		return lang
	}
	return Unknown
}

// ExtensionsFor returns the registered file extensions (with leading dot) for a
// canonical language token, or nil for an unknown/empty token. It is the
// inverse of LanguageForPath, used by detector scoping to build the set of
// extensions a `languages = [...]` species should scan. The result is freshly
// allocated each call (no shared mutable slice) and order is not guaranteed.
func ExtensionsFor(language string) []string {
	if language == "" || language == Unknown {
		return nil
	}
	var exts []string
	for ext, lang := range extToLanguage {
		if lang == language {
			exts = append(exts, ext)
		}
	}
	return exts
}
