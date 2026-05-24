package verify_test

import (
	"context"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/verify"
)

// diffOfLines builds a ProposedDiff whose single file patch adds n lines, so a
// test can dial the changed-line count precisely.
func diffOfLines(path string, n int) engine.ProposedDiff {
	var b strings.Builder
	b.WriteString("--- a/" + path + "\n")
	b.WriteString("+++ b/" + path + "\n")
	b.WriteString("@@ -1,0 +1," + itoa(n) + " @@\n")
	for i := 0; i < n; i++ {
		b.WriteString("+added line\n")
	}
	return engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: path, Patch: b.String()}},
		Fixer: "test",
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

// TestDiffBoundedPassesSmallDiff: a localized diff well under the limits passes.
func TestDiffBoundedPassesSmallDiff(t *testing.T) {
	v := verify.NewDiffBounded(verify.DefaultLimits())
	res := v.Verify(context.Background(), diffOfLines("a.go", 3), engine.Scope{})
	if !res.Passed {
		t.Fatalf("small diff should pass; got %+v", res)
	}
	if len(res.Checks) != 1 || res.Checks[0].Name != verify.CheckDiffBounded {
		t.Fatalf("expected one %q check; got %+v", verify.CheckDiffBounded, res.Checks)
	}
	if !res.Checks[0].Passed {
		t.Errorf("check should be marked passed")
	}
}

// TestDiffBoundedRejectsTooManyLines: a diff over the line limit fails with a
// detail naming the limit (the skip reason must be concrete).
func TestDiffBoundedRejectsTooManyLines(t *testing.T) {
	v := verify.NewDiffBounded(verify.Limits{MaxChangedLines: 5})
	res := v.Verify(context.Background(), diffOfLines("a.go", 6), engine.Scope{})
	if res.Passed {
		t.Fatal("oversized diff (6 lines > 5) must fail")
	}
	c := res.Checks[0]
	if c.Passed || c.Name != verify.CheckDiffBounded {
		t.Fatalf("expected failing %q check; got %+v", verify.CheckDiffBounded, c)
	}
	if !strings.Contains(c.Detail, "6") || !strings.Contains(c.Detail, "5") {
		t.Errorf("detail should name the count and limit; got %q", c.Detail)
	}
}

// TestDiffBoundedRejectsTooManyFiles: a diff touching more files than allowed
// fails on the file dimension.
func TestDiffBoundedRejectsTooManyFiles(t *testing.T) {
	v := verify.NewDiffBounded(verify.Limits{MaxChangedFiles: 2, MaxChangedLines: 1000})
	diff := engine.ProposedDiff{
		Files: []engine.FileDiff{
			{Path: "a.go", Patch: "--- a/a.go\n+++ b/a.go\n@@ -1,0 +1,1 @@\n+x\n"},
			{Path: "b.go", Patch: "--- a/b.go\n+++ b/b.go\n@@ -1,0 +1,1 @@\n+x\n"},
			{Path: "c.go", Patch: "--- a/c.go\n+++ b/c.go\n@@ -1,0 +1,1 @@\n+x\n"},
		},
		Fixer: "test",
	}
	res := v.Verify(context.Background(), diff, engine.Scope{})
	if res.Passed {
		t.Fatal("diff touching 3 files (> 2) must fail")
	}
	if !strings.Contains(res.Checks[0].Detail, "files") {
		t.Errorf("detail should name the file limit; got %q", res.Checks[0].Detail)
	}
}

// TestDiffBoundedUnboundedWhenZero: zero limits mean unbounded — a huge diff
// still passes (lets a caller disable one dimension).
func TestDiffBoundedUnboundedWhenZero(t *testing.T) {
	v := verify.NewDiffBounded(verify.Limits{})
	res := v.Verify(context.Background(), diffOfLines("a.go", 10_000), engine.Scope{})
	if !res.Passed {
		t.Fatalf("zero limits mean unbounded; a large diff should pass; got %+v", res)
	}
}

// recordingVerifier records the order verifiers ran in (shared slice via a
// pointer) and returns a configured result. It is the spike instrument proving
// the chain runs verifiers in order and short-circuits.
type recordingVerifier struct {
	name  string
	pass  bool
	order *[]string
}

func (r recordingVerifier) Verify(context.Context, engine.ProposedDiff, engine.Scope) engine.VerifyResult {
	*r.order = append(*r.order, r.name)
	if r.pass {
		return passResultForTest(r.name)
	}
	return failResultForTest(r.name)
}

func passResultForTest(name string) engine.VerifyResult {
	return engine.VerifyResult{Passed: true, Checks: []engine.CheckResult{{Name: name, Passed: true}}}
}
func failResultForTest(name string) engine.VerifyResult {
	return engine.VerifyResult{Passed: false, Checks: []engine.CheckResult{{Name: name, Passed: false, Detail: name + " failed"}}}
}

// TestChainRunsDiffBoundedFirstAndShortCircuits is the SPIKE proving the gate
// ordering: with diff-bounded placed first and failing, the chain rejects the
// diff WITHOUT ever invoking the expensive downstream verifiers. This validates
// the ordering contract (TECHSPEC §8.1) before the I/O verifiers are wired in.
func TestChainRunsDiffBoundedFirstAndShortCircuits(t *testing.T) {
	var order []string
	chain := verify.NewChain(
		recordingVerifier{name: verify.CheckDiffBounded, pass: false, order: &order},
		recordingVerifier{name: "compile", pass: true, order: &order},
		recordingVerifier{name: "detector-clears", pass: true, order: &order},
	)
	res := chain.Verify(context.Background(), diffOfLines("a.go", 1), engine.Scope{})

	if res.Passed {
		t.Fatal("chain must fail when diff-bounded (first gate) fails")
	}
	if len(order) != 1 || order[0] != verify.CheckDiffBounded {
		t.Fatalf("diff-bounded must run FIRST and short-circuit; ran: %v", order)
	}
	// The surfaced failing check is diff-bounded, not a later gate.
	if res.Checks[len(res.Checks)-1].Name != verify.CheckDiffBounded {
		t.Errorf("failing check should be diff-bounded; got %+v", res.Checks)
	}
}

// TestChainAllPassRunsInOrder: when every gate passes, the chain runs them in
// the given order and aggregates all checks.
func TestChainAllPassRunsInOrder(t *testing.T) {
	var order []string
	chain := verify.NewChain(
		recordingVerifier{name: verify.CheckDiffBounded, pass: true, order: &order},
		recordingVerifier{name: "compile", pass: true, order: &order},
		recordingVerifier{name: "detector-clears", pass: true, order: &order},
	)
	res := chain.Verify(context.Background(), diffOfLines("a.go", 1), engine.Scope{})
	if !res.Passed {
		t.Fatalf("all gates pass → chain passes; got %+v", res)
	}
	want := []string{verify.CheckDiffBounded, "compile", "detector-clears"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("verifier order = %v, want %v", order, want)
	}
	if len(res.Checks) != 3 {
		t.Errorf("expected 3 aggregated checks; got %d", len(res.Checks))
	}
}
