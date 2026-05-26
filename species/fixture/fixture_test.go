package fixture_test

import (
	"path/filepath"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/species/fixture"
)

// speciesRoot is the on-disk built-in species tree, relative to this test
// package (species/fixture → species). The harness loads each species' real
// species.toml + detect.yml from here through the production loader/registry, so
// the fixtures assert the genuine embedded manifests, not test copies.
const speciesRoot = ".."

// cases enumerates every built-in deterministic species (M2, ADR-0002) wired
// through the ONE reusable harness. Adding the M3 LLM species is a new entry here
// plus a recorded FixerFactory — no new test machinery.
func cases() []fixture.Case {
	return []fixture.Case{
		{
			Name:       "unused-import",
			SpeciesDir: filepath.Join(speciesRoot, "unused-import"),
			RepoDir:    filepath.Join("testdata", "unused-import", "repo"),
			GoldenPath: filepath.Join("testdata", "unused-import", "fix.golden"),
		},
		{
			Name:       "dead-code",
			SpeciesDir: filepath.Join(speciesRoot, "dead-code"),
			RepoDir:    filepath.Join("testdata", "dead-code", "repo"),
			GoldenPath: filepath.Join("testdata", "dead-code", "fix.golden"),
		},
		{
			Name:       "unused-variable",
			SpeciesDir: filepath.Join(speciesRoot, "unused-variable"),
			RepoDir:    filepath.Join("testdata", "unused-variable", "repo"),
			GoldenPath: filepath.Join("testdata", "unused-variable", "fix.golden"),
		},
		{
			Name:       "redundant-conversion",
			SpeciesDir: filepath.Join(speciesRoot, "redundant-conversion"),
			RepoDir:    filepath.Join("testdata", "redundant-conversion", "repo"),
			GoldenPath: filepath.Join("testdata", "redundant-conversion", "fix.golden"),
		},
		{
			Name:       "unreachable-code",
			SpeciesDir: filepath.Join(speciesRoot, "unreachable-code"),
			RepoDir:    filepath.Join("testdata", "unreachable-code", "repo"),
			GoldenPath: filepath.Join("testdata", "unreachable-code", "fix.golden"),
		},
		{
			// empty-block, duplicate-condition, redundant-nil-check, and
			// ineffective-assignment are PROPOSE-ONLY species (auto_apply = false).
			// The fixture harness asserts the detect→fix→verify→golden pipeline (the
			// proposed diff) regardless of trust; the propose-only trust default is
			// asserted separately by the embed_test.go adr0002 table
			// (EffectiveAutoApply == false), and the staged-not-applied-under---apply
			// behavior is proven generically by the colony/species trust tests.
			Name:       "empty-block",
			SpeciesDir: filepath.Join(speciesRoot, "empty-block"),
			RepoDir:    filepath.Join("testdata", "empty-block", "repo"),
			GoldenPath: filepath.Join("testdata", "empty-block", "fix.golden"),
		},
		{
			Name:       "duplicate-condition",
			SpeciesDir: filepath.Join(speciesRoot, "duplicate-condition"),
			RepoDir:    filepath.Join("testdata", "duplicate-condition", "repo"),
			GoldenPath: filepath.Join("testdata", "duplicate-condition", "fix.golden"),
		},
		{
			Name:       "redundant-nil-check",
			SpeciesDir: filepath.Join(speciesRoot, "redundant-nil-check"),
			RepoDir:    filepath.Join("testdata", "redundant-nil-check", "repo"),
			GoldenPath: filepath.Join("testdata", "redundant-nil-check", "fix.golden"),
		},
		{
			Name:       "ineffective-assignment",
			SpeciesDir: filepath.Join(speciesRoot, "ineffective-assignment"),
			RepoDir:    filepath.Join("testdata", "ineffective-assignment", "repo"),
			GoldenPath: filepath.Join("testdata", "ineffective-assignment", "fix.golden"),
		},
		{
			Name:       "nil-deref",
			SpeciesDir: filepath.Join(speciesRoot, "nil-deref"),
			RepoDir:    filepath.Join("testdata", "nil-deref", "repo"),
			GoldenPath: filepath.Join("testdata", "nil-deref", "fix.golden"),
			Fixer:      fixture.RecordedFixer(engine.FileDiff{Path: "repo.go", Patch: nilDerefPatch}),
		},
		{
			Name:       "n+1-query",
			SpeciesDir: filepath.Join(speciesRoot, "n+1-query"),
			RepoDir:    filepath.Join("testdata", "n+1-query", "repo"),
			GoldenPath: filepath.Join("testdata", "n+1-query", "fix.golden"),
			Fixer:      fixture.RecordedFixer(engine.FileDiff{Path: "repo.go", Patch: nPlusOnePatch}),
		},
		{
			Name:       "missing-await",
			SpeciesDir: filepath.Join(speciesRoot, "missing-await"),
			RepoDir:    filepath.Join("testdata", "missing-await", "repo"),
			GoldenPath: filepath.Join("testdata", "missing-await", "fix.golden"),
			Fixer:      fixture.RecordedFixer(engine.FileDiff{Path: "repo.go", Patch: missingAwaitPatch}),
		},
		{
			// ai-slop ships DISABLED by default (species.toml enabled = false), but
			// the harness drives its detect→fix→verify→golden path directly: the
			// pipeline operates on the species regardless of the runtime enabled
			// flag (enabled=false is a resolution-time concern, not a harness one).
			// A separate test (TestAISlopShipsDisabled) confirms it still resolves as
			// disabled in a normal run.
			Name:       "ai-slop",
			SpeciesDir: filepath.Join(speciesRoot, "ai-slop"),
			RepoDir:    filepath.Join("testdata", "ai-slop", "repo"),
			GoldenPath: filepath.Join("testdata", "ai-slop", "fix.golden"),
			Fixer:      fixture.RecordedFixer(engine.FileDiff{Path: "repo.go", Patch: aiSlopPatch}),
		},
	}
}

// The three recorded LLM fix responses (TECHSPEC §10 — no live model in CI).
// Each is the unified-diff patch a correctly-prompted model would return for the
// fixture's single finding; the harness drives it through the REAL verifier gate
// (compile + tests:affected + detector-clears), so the recorded fix is accepted
// only if it genuinely compiles, keeps the affected tests green, AND clears the
// detector. The patch body is the golden, so a drift in either fails the test.

// nilDerefPatch binds and checks the discarded error, returning (int, error) so
// the nil dereference is guarded.
const nilDerefPatch = `--- a/repo.go
+++ b/repo.go
@@ -22,4 +22,8 @@
-func Balance(id int) int {
-	acct, _ := loadAccount(id)
-	return acct.Balance
-}
+func Balance(id int) (int, error) {
+	acct, err := loadAccount(id)
+	if err != nil {
+		return 0, err
+	}
+	return acct.Balance, nil
+}
`

// nPlusOnePatch hoists the per-iteration lookup out of the loop into a single
// batched query before the loop, eliminating the N+1.
const nPlusOnePatch = `--- a/repo.go
+++ b/repo.go
@@ -32,5 +32,6 @@
-	var names []string
-	for _, id := range ids {
-		u := lookupUser(id)
-		names = append(names, u.Name)
-	}
+	users := lookupUsers(ids)
+	var names []string
+	for _, u := range users {
+		names = append(names, u.Name)
+	}
`

// missingAwaitPatch adds the sync import, then captures each goroutine's result
// in a per-index slice, waits on a WaitGroup, and sums — so the spawned work is
// awaited and race-free instead of dropped. Two hunks: the import insert, then
// the loop rewrite.
const missingAwaitPatch = `--- a/repo.go
+++ b/repo.go
@@ -1,1 +1,3 @@
 package missingawait
+
+import "sync"
@@ -16,6 +18,11 @@
 	var total int
-	for _, n := range nums {
-		go func(n int) {
-			total += square(n)
-		}(n)
-	}
+	results := make([]int, len(nums))
+	var wg sync.WaitGroup
+	for i, n := range nums {
+		wg.Add(1)
+		go func(i, n int) {
+			defer wg.Done()
+			results[i] = square(n)
+		}(i, n)
+	}
+	wg.Wait()
+	for _, r := range results {
+		total += r
+	}
`

// aiSlopPatch inlines the redundant temporary `result` into a direct
// `return a + b`, removing the AI-boilerplate tic the ai-slop detector
// nominated. After this fix the `$V := $EXPR` / `return $V` shape is gone, so
// detector-clears reports zero matches; compile + tests:affected confirm Sum's
// behavior is unchanged. (ai-slop is fuzzy/candidate-tier and ships disabled —
// see the case comment and TestAISlopShipsDisabled.)
const aiSlopPatch = "--- a/repo.go\n" +
	"+++ b/repo.go\n" +
	"@@ -16,2 +16,1 @@\n" +
	"-\tresult := a + b\n" +
	"-\treturn result\n" +
	"+\treturn a + b\n"

// TestBuiltinSpeciesFixtures runs the detect→fix→verify→golden harness over each
// built-in deterministic species with the REAL ast-grep detector, the REAL
// delete-match fixer, and the REAL compile + detector-clears verifier gate. When
// ast-grep is not installed every case skips (detection is a plugin boundary,
// TECHSPEC §2), so the suite stays green without the binary while proving genuine
// end-to-end behavior where it is present.
func TestBuiltinSpeciesFixtures(t *testing.T) {
	for _, c := range cases() {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			fixture.RunCase(t, c)
		})
	}
}
