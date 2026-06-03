package langmap_test

import (
	"sort"
	"testing"

	"github.com/gitpcl/ant/internal/engine/langmap"
)

// TestLanguageForPath is the table-driven authority test: every registered
// extension resolves to its canonical language, and anything else is the stable
// Unknown sentinel (the honest-skip signal the verifiers depend on).
func TestLanguageForPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"main.go", langmap.Go},
		{"internal/engine/types.go", langmap.Go},
		{"app/Models/User.php", langmap.PHP},
		{"src/util.py", langmap.Python},
		{"components/App.ts", langmap.TypeScript},
		{"components/App.tsx", langmap.TypeScript},
		{"legacy/index.js", langmap.JavaScript},
		{"legacy/index.jsx", langmap.JavaScript},
		{"views/Home.vue", langmap.Vue},
		// Case-insensitive extension.
		{"SHOUT.PHP", langmap.PHP},
		{"Mixed.Ts", langmap.TypeScript},
		// Unknown / unregistered.
		{"README.md", langmap.Unknown},
		{"Makefile", langmap.Unknown},
		{"archive.tar.gz", langmap.Unknown},
		{"noext", langmap.Unknown},
		{"", langmap.Unknown},
	}
	for _, c := range cases {
		if got := langmap.LanguageForPath(c.path); got != c.want {
			t.Errorf("LanguageForPath(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// TestExtensionsFor verifies the inverse map used by detector scoping: a
// language token yields exactly its registered extensions, and an
// unknown/empty token yields nil.
func TestExtensionsFor(t *testing.T) {
	cases := []struct {
		language string
		want     []string
	}{
		{langmap.Go, []string{".go"}},
		{langmap.PHP, []string{".php"}},
		{langmap.Python, []string{".py"}},
		{langmap.TypeScript, []string{".ts", ".tsx"}},
		{langmap.JavaScript, []string{".js", ".jsx"}},
		{langmap.Vue, []string{".vue"}},
		{langmap.Unknown, nil},
		{"", nil},
		{"cobol", nil},
	}
	for _, c := range cases {
		got := langmap.ExtensionsFor(c.language)
		sort.Strings(got)
		sort.Strings(c.want)
		if !equalStrings(got, c.want) {
			t.Errorf("ExtensionsFor(%q) = %v, want %v", c.language, got, c.want)
		}
	}
}

// TestLangmapRoundTrip asserts every extension's language maps back to a set
// that contains that extension, so the two directions never diverge.
func TestLangmapRoundTrip(t *testing.T) {
	for _, ext := range []string{".go", ".php", ".py", ".ts", ".tsx", ".js", ".jsx", ".vue"} {
		lang := langmap.LanguageForPath("file" + ext)
		if lang == langmap.Unknown {
			t.Fatalf("extension %q resolved to Unknown", ext)
		}
		exts := langmap.ExtensionsFor(lang)
		if !contains(exts, ext) {
			t.Errorf("ExtensionsFor(%q) = %v, missing %q", lang, exts, ext)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
