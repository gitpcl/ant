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
