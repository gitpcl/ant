package apply

import "testing"

func TestApplyUnifiedPatchDeleteMatch(t *testing.T) {
	// A delete-match patch (the deterministic fixer's output) removing one line.
	src := "package main\n\nimport \"fmt\"\n\nfunc main() {}\n"
	patch := "--- a/main.go\n+++ b/main.go\n@@ -3,1 +3,0 @@\n-import \"fmt\"\n"
	got, err := applyUnifiedPatch(src, patch)
	if err != nil {
		t.Fatalf("applyUnifiedPatch: %v", err)
	}
	want := "package main\n\n\nfunc main() {}\n"
	if got != want {
		t.Errorf("delete-match apply mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestApplyUnifiedPatchReplaceHunk(t *testing.T) {
	src := "a\nb\nc\nd\n"
	// Replace line 2 "b" with "B" and "B2", keeping context.
	patch := "--- a/x\n+++ b/x\n@@ -1,3 +1,4 @@\n a\n-b\n+B\n+B2\n c\n"
	got, err := applyUnifiedPatch(src, patch)
	if err != nil {
		t.Fatalf("applyUnifiedPatch: %v", err)
	}
	want := "a\nB\nB2\nc\nd\n"
	if got != want {
		t.Errorf("replace apply mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestApplyUnifiedPatchContextMismatchFails(t *testing.T) {
	src := "a\nb\nc\n"
	// Patch expects "X" where the file has "b" — stale patch must fail loudly.
	patch := "@@ -2,1 +2,1 @@\n-X\n+Y\n"
	if _, err := applyUnifiedPatch(src, patch); err == nil {
		t.Error("a patch whose removed line does not match the source must fail (never corrupt the file)")
	}
}

func TestApplyUnifiedPatchHeadersOnlyIsNoop(t *testing.T) {
	src := "unchanged\n"
	got, err := applyUnifiedPatch(src, "--- a/x\n+++ b/x\n")
	if err != nil {
		t.Fatalf("headers-only patch: %v", err)
	}
	if got != src {
		t.Errorf("headers-only patch should be a no-op, got %q", got)
	}
}
