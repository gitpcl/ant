package engine

import "testing"

func TestPathIgnored(t *testing.T) {
	globs := []string{"vendor/", "node_modules/", "*_generated.go", "pkg/gen/"}
	cases := []struct {
		file string
		want bool
	}{
		{"vendor/foo/bar.go", true},        // directory prefix
		{"vendor", true},                   // the dir itself
		{"node_modules/x/y.js", true},      // second directory prefix
		{"src/types_generated.go", true},   // basename glob anywhere
		{"types_generated.go", true},       // basename glob at root
		{"pkg/gen/code.go", true},          // nested directory prefix
		{"src/app.go", false},              // not ignored
		{"vendoring/app.go", false},        // prefix must be a path boundary
		{"src/generated_helper.go", false}, // glob anchored, no match
	}
	for _, c := range cases {
		if got := PathIgnored(c.file, globs); got != c.want {
			t.Errorf("PathIgnored(%q): got %v, want %v", c.file, got, c.want)
		}
	}
}

func TestPathIgnoredEmptyGlobs(t *testing.T) {
	if PathIgnored("anything.go", nil) {
		t.Error("no globs must ignore nothing")
	}
}

func TestFilterIgnored(t *testing.T) {
	findings := []Finding{
		{Species: "a", File: "vendor/x.go"},
		{Species: "b", File: "src/main.go"},
		{Species: "c", File: "src/types_generated.go"},
	}
	got := FilterIgnored(findings, []string{"vendor/", "*_generated.go"})
	if len(got) != 1 || got[0].File != "src/main.go" {
		t.Fatalf("FilterIgnored kept %+v, want only src/main.go", got)
	}
	// Input must not be mutated.
	if len(findings) != 3 {
		t.Errorf("input mutated: len=%d, want 3", len(findings))
	}
}

func TestFilterIgnoredNoGlobs(t *testing.T) {
	findings := []Finding{{File: "a.go"}, {File: "b.go"}}
	got := FilterIgnored(findings, nil)
	if len(got) != 2 {
		t.Fatalf("no globs must keep all findings, got %d", len(got))
	}
}

// TestPathIgnoredSegmentGlob covers the "**/SEGMENT/**" default-ignore form: it
// ignores a noise dir wherever it appears as a path SEGMENT, but a literal file
// whose name merely contains the segment is NOT ignored, and the segment must be
// a whole path element (not a substring).
func TestPathIgnoredSegmentGlob(t *testing.T) {
	globs := []string{"**/vendor/**", "**/node_modules/**", "**/.git/**", "**/testdata/**"}
	cases := []struct {
		file string
		want bool
	}{
		{"vendor/x.go", true},                        // segment at root
		{"internal/foo/vendor/bar.go", true},         // nested vendor
		{"web/node_modules/pkg/index.js", true},      // nested node_modules
		{"a/.git/HEAD", true},                        // nested .git
		{"species/fixture/testdata/x/repo.go", true}, // nested testdata
		{"src/app.go", false},                        // no noise segment
		{"internal/vendored/app.go", false},          // substring, not a segment
		{"cmd/testdata_helper.go", false},            // filename contains, not a segment
		{"digit/main.go", false},                     // "digit" != ".git" segment
	}
	for _, c := range cases {
		if got := PathIgnored(c.file, globs); got != c.want {
			t.Errorf("PathIgnored(%q): got %v, want %v", c.file, got, c.want)
		}
	}
}

// TestSegmentGlobRootVsNested is the SUBTLE correctness point: the default
// "**/testdata/**" must suppress testdata NESTED below the scan root but NEVER a
// run that scans INTO a testdata dir. FilterIgnored matches on the root-RELATIVE
// finding File: scanning `./pkg/testdata/foo` yields files like "bar.go" with no
// "testdata" segment, so they survive, while a whole-repo scan's
// "pkg/testdata/bar.go" is suppressed.
func TestSegmentGlobRootVsNested(t *testing.T) {
	globs := []string{"**/testdata/**"}

	// Whole-repo scan: the finding File carries the testdata segment → suppressed.
	nested := FilterIgnored([]Finding{{File: "pkg/testdata/bar.go"}}, globs)
	if len(nested) != 0 {
		t.Errorf("nested testdata must be suppressed, kept %+v", nested)
	}

	// Scan rooted INSIDE testdata: the File is relative to that root (no testdata
	// segment) → must STILL be reported.
	rooted := FilterIgnored([]Finding{{File: "bar.go"}, {File: "sub/baz.go"}}, globs)
	if len(rooted) != 2 {
		t.Errorf("scanning INTO testdata must still report findings, kept %+v", rooted)
	}
}
