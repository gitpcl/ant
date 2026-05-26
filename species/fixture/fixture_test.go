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
			// trailing-debug-code is PROPOSE-ONLY (auto_apply=false) but its fix is a
			// deterministic delete-match (remove the fmt.Println debug line), so it
			// runs through the default DeterministicFixer like the other cleanup
			// species — no tool override. compile + detector-clears gate it.
			Name:       "trailing-debug-code",
			SpeciesDir: filepath.Join(speciesRoot, "trailing-debug-code"),
			RepoDir:    filepath.Join("testdata", "trailing-debug-code", "repo"),
			GoldenPath: filepath.Join("testdata", "trailing-debug-code", "fix.golden"),
		},
		{
			// ignored-error is the Sprint 018 bug-risk FLAGSHIP (Go): the detector
			// nominates a `v, _ := call()` error discard; the recorded fix binds and
			// propagates the error (changing the signature to return error). The gate
			// (compile + tests:affected + detector-clears) confirms the rewrite
			// compiles, keeps the affected test green, and leaves no discard behind.
			Name:       "ignored-error",
			SpeciesDir: filepath.Join(speciesRoot, "ignored-error"),
			RepoDir:    filepath.Join("testdata", "ignored-error", "repo"),
			GoldenPath: filepath.Join("testdata", "ignored-error", "fix.golden"),
			Fixer:      fixture.RecordedFixer(engine.FileDiff{Path: "repo.go", Patch: ignoredErrorPatch}),
		},
		{
			// unchecked-type-assertion: detector nominates a single-result `s :=
			// v.(string)`; the recorded fix switches to the comma-ok form and returns
			// an error on the not-ok branch (changing the signature). The gate
			// confirms compile + tests:affected + detector-clears.
			Name:       "unchecked-type-assertion",
			SpeciesDir: filepath.Join(speciesRoot, "unchecked-type-assertion"),
			RepoDir:    filepath.Join("testdata", "unchecked-type-assertion", "repo"),
			GoldenPath: filepath.Join("testdata", "unchecked-type-assertion", "fix.golden"),
			Fixer:      fixture.RecordedFixer(engine.FileDiff{Path: "repo.go", Patch: uncheckedAssertionPatch}),
		},
		{
			// resource-leak is the Sprint 018 SIGNATURE species: the detector nominates
			// a function that opens a file (os.Open) with no Close on any path; the
			// recorded fix adds `defer f.Close()` so the file closes on ALL return paths
			// (the multi-path-close requirement). After the fix the function HAS a Close
			// call, so detector-clears matches zero; compile + tests:affected confirm
			// behavior on both the success and error paths is preserved.
			Name:       "resource-leak",
			SpeciesDir: filepath.Join(speciesRoot, "resource-leak"),
			RepoDir:    filepath.Join("testdata", "resource-leak", "repo"),
			GoldenPath: filepath.Join("testdata", "resource-leak", "fix.golden"),
			Fixer:      fixture.RecordedFixer(engine.FileDiff{Path: "repo.go", Patch: resourceLeakPatch}),
		},
		{
			// missing-context-timeout: detector nominates a call passing
			// context.Background() directly; the recorded fix derives a
			// context.WithTimeout (with defer cancel) and passes that. After the fix the
			// call site no longer passes a literal context.Background(), so
			// detector-clears matches zero.
			Name:       "missing-context-timeout",
			SpeciesDir: filepath.Join(speciesRoot, "missing-context-timeout"),
			RepoDir:    filepath.Join("testdata", "missing-context-timeout", "repo"),
			GoldenPath: filepath.Join("testdata", "missing-context-timeout", "fix.golden"),
			Fixer:      fixture.RecordedFixer(engine.FileDiff{Path: "repo.go", Patch: missingContextTimeoutPatch}),
		},
		{
			// unsafe-concurrency is the PREMIUM/hard species: the detector nominates a
			// function with an unsynchronized `go func` (no sync primitive); the recorded
			// fix adds a sync.Mutex guarding the shared write AND a sync.WaitGroup owning
			// the goroutines' lifecycle. After the fix the function HAS a sync. reference,
			// so detector-clears matches zero; compile + tests:affected confirm the count
			// is now correct, and CI's `go test -race` confirms it is race-free.
			Name:       "unsafe-concurrency",
			SpeciesDir: filepath.Join(speciesRoot, "unsafe-concurrency"),
			RepoDir:    filepath.Join("testdata", "unsafe-concurrency", "repo"),
			GoldenPath: filepath.Join("testdata", "unsafe-concurrency", "fix.golden"),
			Fixer:      fixture.RecordedFixer(engine.FileDiff{Path: "repo.go", Patch: unsafeConcurrencyPatch}),
		},
		{
			// sql-string-concat is the Sprint 018 SECURITY-stage species: the detector
			// nominates a call whose query string is built by concatenating a value into
			// the SQL text (`db.QueryRow("... WHERE id = " + strconv.Itoa(id))`); the
			// recorded fix moves the id into a BOUND `?` parameter (the value travels as
			// data, never as SQL) and drops the now-unused strconv import. After the fix
			// the query is a single static literal with no `+` concatenation, so
			// detector-clears matches zero; compile + tests:affected confirm the
			// parameterized form binds the value and preserves both the success and error
			// paths.
			Name:       "sql-string-concat",
			SpeciesDir: filepath.Join(speciesRoot, "sql-string-concat"),
			RepoDir:    filepath.Join("testdata", "sql-string-concat", "repo"),
			GoldenPath: filepath.Join("testdata", "sql-string-concat", "fix.golden"),
			Fixer:      fixture.RecordedFixer(engine.FileDiff{Path: "repo.go", Patch: sqlStringConcatPatch}),
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

// ignoredErrorPatch (Sprint 018 flagship) binds the discarded error to a named
// `err` and propagates it, changing Port's signature to (int, error) so the
// parse failure is no longer silently swallowed. After the fix the `port, _ :=
// parsePort(raw)` discard is gone, so detector-clears matches zero times.
const ignoredErrorPatch = `--- a/repo.go
+++ b/repo.go
@@ -18,4 +18,8 @@
-func Port(raw string) int {
-	port, _ := parsePort(raw)
-	return port
-}
+func Port(raw string) (int, error) {
+	port, err := parsePort(raw)
+	if err != nil {
+		return 0, err
+	}
+	return port, nil
+}
`

// uncheckedAssertionPatch (Sprint 018) switches the single-result assertion to
// the comma-ok form and returns an error on the not-ok branch, changing
// AsString's signature to (string, error). After the fix the `s := v.(string)`
// single-result form is gone, so detector-clears matches zero.
const uncheckedAssertionPatch = `--- a/repo.go
+++ b/repo.go
@@ -12,4 +12,8 @@
-func AsString(v interface{}) string {
-	s := v.(string)
-	return s
-}
+func AsString(v interface{}) (string, error) {
+	s, ok := v.(string)
+	if !ok {
+		return "", fmt.Errorf("expected string, got %T", v)
+	}
+	return s, nil
+}
`

// resourceLeakPatch (Sprint 018 signature) inserts `defer f.Close()` immediately
// after the open succeeds, so the *os.File is closed on ALL return paths (the
// io.ReadAll error path AND the success path). After the fix the function HAS a
// Close call, so detector-clears matches zero. One hunk: a single inserted line
// with the surrounding open + error-check as context.
const resourceLeakPatch = `--- a/repo.go
+++ b/repo.go
@@ -16,4 +16,5 @@
 	f, err := os.Open(path)
 	if err != nil {
 		return 0, err
 	}
+	defer f.Close()
`

// missingContextTimeoutPatch (Sprint 018) derives a bounded context via
// context.WithTimeout (with defer cancel) and passes it to query instead of the
// inline context.Background(). Two hunks: add the "time" import (grouping it with
// "context"), then rewrite Fetch's body. After the fix the call no longer passes
// a literal context.Background() as its first argument, so detector-clears
// matches zero.
const missingContextTimeoutPatch = `--- a/repo.go
+++ b/repo.go
@@ -3,1 +3,6 @@
-import "context"
+import (
+	"context"
+	"time"
+)
@@ -20,3 +25,5 @@
-func Fetch(key string) (string, error) {
-	return query(context.Background(), key)
-}
+func Fetch(key string) (string, error) {
+	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
+	defer cancel()
+	return query(ctx, key)
+}
`

// unsafeConcurrencyPatch (Sprint 018 premium) adds the "sync" import, then
// rewrites CountUp to guard the shared increment with a sync.Mutex and own the
// goroutines' lifecycle with a sync.WaitGroup (Add/Done/Wait). After the fix the
// function HAS a sync. reference, so detector-clears matches zero; the post-fix
// code is race-free under `go test -race` and deterministically returns n. Two
// hunks: the import insert, then the body rewrite.
const unsafeConcurrencyPatch = `--- a/repo.go
+++ b/repo.go
@@ -1,1 +1,3 @@
 package unsafeconcurrency
+
+import "sync"
@@ -12,9 +14,16 @@
 func CountUp(n int) int {
 	count := 0
-	for i := 0; i < n; i++ {
-		go func() {
-			count++
-		}()
-	}
+	var mu sync.Mutex
+	var wg sync.WaitGroup
+	for i := 0; i < n; i++ {
+		wg.Add(1)
+		go func() {
+			defer wg.Done()
+			mu.Lock()
+			count++
+			mu.Unlock()
+		}()
+	}
+	wg.Wait()
 	return count
 }
`

// sqlStringConcatPatch (Sprint 018 security) moves the concatenated id out of the
// SQL text into a BOUND `?` parameter: the query becomes a single static string
// literal and the id is passed as a trailing argument, so the driver binds it as
// data and it can never be interpreted as SQL (the SQL-injection vector is
// closed). The strconv import is dropped because Itoa is no longer used. After the
// fix the query string has no `+` concatenation, so detector-clears matches zero.
// Two hunks: remove the now-unused import, then rewrite the QueryRow call.
const sqlStringConcatPatch = `--- a/repo.go
+++ b/repo.go
@@ -3,1 +3,0 @@
-import "strconv"
@@ -37,1 +36,1 @@
-	r := s.db.QueryRow("SELECT name FROM users WHERE id = " + strconv.Itoa(id))
+	r := s.db.QueryRow("SELECT name FROM users WHERE id = ?", id)
`

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
