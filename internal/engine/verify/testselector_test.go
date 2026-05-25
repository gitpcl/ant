package verify_test

import (
	"context"
	"strings"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/verify"
	"github.com/gitpcl/ant/internal/engine/verify/testselect"
)

// provenanceVisible mirrors review/view.go isStrategyDetail: a CheckResult.Detail
// only surfaces in the `ant review` PROVENANCE panel when it is short (<=24 chars)
// and contains no "." or newline. The strategy labels are designed to satisfy this
// so the chosen strategy is actually VISIBLE to the developer (TECHSPEC §5.3.1,
// review-interaction.md §3) — this guard keeps the labels honest.
func provenanceVisible(detail string) bool {
	return len(detail) > 0 && len(detail) <= 24 && !strings.ContainsAny(detail, ".\n")
}

// TestSelectorStrategyPropagatesIntoCheckResult is feature 1's acceptance test:
// the selection STRATEGY is recorded in the tests:affected CheckResult.Detail and
// the label is short enough to be visible in review provenance. It drives each of
// the three strategies through the real verifier (with a stub selector + recording
// runner) and asserts the right label propagates AND clears the provenance gate.
func TestSelectorStrategyPropagatesIntoCheckResult(t *testing.T) {
	old, newBody := affectedFixture()

	cases := []struct {
		name     string
		strategy testselect.Strategy
		wantPre  string // expected Detail prefix
	}{
		{"coverage-map", testselect.StrategyCoverageMap, "coverage-map"},
		{"import-graph", testselect.StrategyImportGraph, "import-graph"},
		{"package-fallback", testselect.StrategyPackageFallback, "package-fallback"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeModule(t, root, old)
			diff := engine.ProposedDiff{
				Files: []engine.FileDiff{{Path: "main.go", Patch: replaceFilePatch("main.go", old, newBody)}},
				Fixer: "test",
			}
			runner := &recordingRunner{}
			v := verify.NewTestsAffectedWith(runner.run, stubSelector{sel: testselect.Selection{
				Packages: []string{"./pkg/x"}, Tests: []string{"./pkg/x"},
				Strategy: tc.strategy, OK: true,
			}})

			res := v.Verify(context.Background(), diff, engine.Scope{Root: root})
			if !res.Passed || len(res.Checks) != 1 {
				t.Fatalf("expected one passed check, got %+v", res)
			}
			c := res.Checks[0]
			if c.Name != verify.CheckTestsAffected {
				t.Errorf("check name = %q, want %q", c.Name, verify.CheckTestsAffected)
			}
			if !strings.HasPrefix(c.Detail, tc.wantPre) {
				t.Errorf("Detail = %q, want it to report the %q strategy", c.Detail, tc.wantPre)
			}
			if !provenanceVisible(c.Detail) {
				t.Errorf("Detail %q (len %d) would NOT surface in review provenance (needs <=24 chars, no '.'/newline)", c.Detail, len(c.Detail))
			}
		})
	}
}

// TestSelectorInterfaceIsImplementedByAllThree is the structural half of feature 1:
// all three strategies satisfy the TestSelector interface (so a per-language
// selector is pluggable), proven by assigning each constructor's result to the
// interface type.
func TestSelectorInterfaceIsImplementedByAllThree(t *testing.T) {
	var _ testselect.TestSelector = testselect.NewImportGraph(nil)
	var _ testselect.TestSelector = testselect.NewPackageFallback()
	var _ testselect.TestSelector = testselect.NewCoverage(testselect.NewProfileCache(testselect.NewGoProfileGenerator()))

	// And the Label() reporting is well-formed for each strategy.
	for _, s := range []testselect.Strategy{
		testselect.StrategyCoverageMap, testselect.StrategyImportGraph, testselect.StrategyPackageFallback,
	} {
		sel := testselect.Selection{Strategy: s, Tests: []string{"a"}, Packages: []string{"a"}}
		if !strings.HasPrefix(sel.Label(), string(s)) {
			t.Errorf("Label() %q does not start with strategy %q", sel.Label(), s)
		}
	}
}
