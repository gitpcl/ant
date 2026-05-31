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
			// long-function (Sprint 019): the detector nominates a function whose body
			// exceeds the statement threshold (6); the recorded fix extracts a cohesive
			// block into a `products` helper, leaving BOTH functions below the threshold
			// and computing the identical result. After the fix no function exceeds the
			// threshold, so detector-clears matches zero; compile + tests:affected
			// confirm Process's result is unchanged.
			Name:       "long-function",
			SpeciesDir: filepath.Join(speciesRoot, "long-function"),
			RepoDir:    filepath.Join("testdata", "long-function", "repo"),
			GoldenPath: filepath.Join("testdata", "long-function", "fix.golden"),
			Fixer:      fixture.RecordedFixer(engine.FileDiff{Path: "repo.go", Patch: longFunctionPatch}),
		},
		{
			// magic-number (Sprint 019): the detector nominates repeated unexplained
			// numeric literals (multi-digit, excluding trivial single digits); the
			// recorded fix extracts a named constant and replaces the occurrences. After
			// the fix the literal is gone (only the named constant's single definition
			// remains), so detector-clears matches zero; compile + tests:affected confirm
			// the value is unchanged.
			Name:       "magic-number",
			SpeciesDir: filepath.Join(speciesRoot, "magic-number"),
			RepoDir:    filepath.Join("testdata", "magic-number", "repo"),
			GoldenPath: filepath.Join("testdata", "magic-number", "fix.golden"),
			Fixer:      fixture.RecordedFixer(engine.FileDiff{Path: "repo.go", Patch: magicNumberPatch}),
		},
		{
			// duplicate-code-small (Sprint 019): the detector nominates a small repeated
			// block that appears in two functions; the recorded fix extracts a shared
			// helper and calls it from both sites. After the fix the duplicated block
			// appears once (in the helper), so detector-clears matches zero; compile +
			// tests:affected confirm both callers still compute the same result.
			Name:       "duplicate-code-small",
			SpeciesDir: filepath.Join(speciesRoot, "duplicate-code-small"),
			RepoDir:    filepath.Join("testdata", "duplicate-code-small", "repo"),
			GoldenPath: filepath.Join("testdata", "duplicate-code-small", "fix.golden"),
			Fixer:      fixture.RecordedFixer(engine.FileDiff{Path: "repo.go", Patch: duplicateCodeSmallPatch}),
		},
		{
			// deep-nesting is the Sprint 019 SIGNATURE species (the verified-refactor
			// showcase): the detector nominates the OUTERMOST `if` of a depth-3 nest;
			// the recorded fix flattens it to guard clauses / early returns, preserving
			// the exact result on every path. After the fix no depth-3 nest remains, so
			// detector-clears matches zero; compile + tests:affected confirm every path
			// (success + each early-exit) keeps its original return value.
			Name:       "deep-nesting",
			SpeciesDir: filepath.Join(speciesRoot, "deep-nesting"),
			RepoDir:    filepath.Join("testdata", "deep-nesting", "repo"),
			GoldenPath: filepath.Join("testdata", "deep-nesting", "fix.golden"),
			Fixer:      fixture.RecordedFixer(engine.FileDiff{Path: "repo.go", Patch: deepNestingPatch}),
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
		{
			// unused-dependency is the Sprint 020 SIGNATURE deps species and the FIRST
			// command-detector + command:-verifier species. The detector (detect.sh)
			// cross-references go.mod requires vs the imports used across the tree —
			// analysis ast-grep cannot do — and flags `rsc.io/quote`, which the fixture
			// declares but never imports. The deterministic delete-match fix removes the
			// require line; the command: verifier (verify.sh) runs `go build`/`go vet`
			// on the scratch copy to prove the module still compiles offline with NO
			// external dependency, then detector-clears confirms the require is gone.
			// Propose-only (auto_apply=false). Hermetic: the post-fix module has only a
			// stdlib import, so the gate resolves entirely offline.
			Name:       "unused-dependency",
			SpeciesDir: filepath.Join(speciesRoot, "unused-dependency"),
			RepoDir:    filepath.Join("testdata", "unused-dependency", "repo"),
			GoldenPath: filepath.Join("testdata", "unused-dependency", "fix.golden"),
		},
		{
			// stale-dependency-pin (Sprint 020 deps): the command detector flags a
			// require duplicated in go.mod (the same module pinned twice); the
			// deterministic delete-match fix removes the redundant require line; the
			// command: verifier runs `go build`/`go vet` on the scratch copy to prove
			// the kept pin is sufficient, then detector-clears confirms the duplicate is
			// gone. Hermetic: the module imports only stdlib, so the gate is offline.
			Name:       "stale-dependency-pin",
			SpeciesDir: filepath.Join(speciesRoot, "stale-dependency-pin"),
			RepoDir:    filepath.Join("testdata", "stale-dependency-pin", "repo"),
			GoldenPath: filepath.Join("testdata", "stale-dependency-pin", "fix.golden"),
		},
		{
			// dead-config (Sprint 020 deps): the command detector flags a config.json
			// key referenced nowhere in the tree (an orphan); the deterministic
			// delete-match fix removes the key line (its trailing comma goes with it, so
			// the JSON stays valid); the command: verifier PARSES config.json (python3
			// json.load) to prove it is still valid JSON. RequiredTools=[python3] so CI
			// without python3 skips, exactly as ast-grep species skip without ast-grep.
			Name:          "dead-config",
			SpeciesDir:    filepath.Join(speciesRoot, "dead-config"),
			RepoDir:       filepath.Join("testdata", "dead-config", "repo"),
			GoldenPath:    filepath.Join("testdata", "dead-config", "fix.golden"),
			RequiredTools: []string{"python3"},
		},
		{
			// duplicate-ci-step (Sprint 020 deps): the command detector flags a `- run:`
			// step duplicated across jobs in .github/workflows/ci.yml; the deterministic
			// delete-match fix removes the duplicate step line; the command: verifier
			// runs a pure-stdlib YAML structural lint (python3) to prove the workflow
			// stays parseable (no tabs, even indentation, no emptied `steps:`).
			// RequiredTools=[python3] gates the case on python3 like dead-config.
			Name:          "duplicate-ci-step",
			SpeciesDir:    filepath.Join(speciesRoot, "duplicate-ci-step"),
			RepoDir:       filepath.Join("testdata", "duplicate-ci-step", "repo"),
			GoldenPath:    filepath.Join("testdata", "duplicate-ci-step", "fix.golden"),
			RequiredTools: []string{"python3"},
		},
		{
			// hardcoded-secret is the Sprint 021 P6 SECURITY-stage SIGNATURE species:
			// remediation, not just detection. The command detector (detect.sh) flags
			// a hardcoded AWS access key id (the official AKIAIOSFODNN7EXAMPLE FAKE)
			// assigned in config.go; the recorded LLM fix is MULTI-FILE — it replaces
			// the literal with os.Getenv("AWS_ACCESS_KEY_ID") (adding `import "os"`)
			// AND records the variable in .env.example. The verifier gate is the
			// remediation proof: compile (the rewrite builds) + command:scan.sh (a
			// SECRET-SCANNER that re-runs over the post-fix scratch tree and exits
			// non-zero if ANY secret remains — it CLEARS here) + detector-clears (the
			// species' own detector matches zero). Propose-only (auto_apply=false). The
			// fixture contains ONLY an obvious fake; the scanner is a hermetic stub, so
			// CI needs no installed scanner.
			Name:       "hardcoded-secret",
			SpeciesDir: filepath.Join(speciesRoot, "hardcoded-secret"),
			RepoDir:    filepath.Join("testdata", "hardcoded-secret", "repo"),
			GoldenPath: filepath.Join("testdata", "hardcoded-secret", "fix.golden"),
			Fixer: fixture.RecordedFixerMulti(
				engine.FileDiff{Path: ".env.example", Patch: hardcodedSecretEnvPatch},
				engine.FileDiff{Path: "config.go", Patch: hardcodedSecretConfigPatch},
			),
		},
		{
			// insecure-random is a Sprint 021 P6 SECURITY-stage species: a security-
			// sensitive value (a session token) generated with math/rand is predictable.
			// The ast-grep detector nominates a call to rand.Intn/Int63/… inside a
			// security context; the recorded fix replaces the weak RNG with crypto/rand
			// (rand.Read over a crypto-strong byte buffer) so the value is
			// unpredictable. After the fix no math/rand call remains, so detector-clears
			// matches zero; compile + tests:affected confirm the token is still produced
			// and the right length. Propose-only (auto_apply=false).
			Name:       "insecure-random",
			SpeciesDir: filepath.Join(speciesRoot, "insecure-random"),
			RepoDir:    filepath.Join("testdata", "insecure-random", "repo"),
			GoldenPath: filepath.Join("testdata", "insecure-random", "fix.golden"),
			Fixer:      fixture.RecordedFixer(engine.FileDiff{Path: "repo.go", Patch: insecureRandomPatch}),
		},
		{
			// unsafe-temp-file is a Sprint 021 P6 SECURITY-stage species: a temp file
			// written to a PREDICTABLE path under /tmp is a symlink/race/clobber risk.
			// The ast-grep detector nominates the hardcoded "/tmp/..." path argument;
			// the recorded fix switches to os.CreateTemp (the OS picks an unpredictable
			// name with 0600 perms and returns an open handle). After the fix no
			// hardcoded /tmp literal remains, so detector-clears matches zero; compile +
			// tests:affected confirm a temp file is still created and written.
			// Propose-only (auto_apply=false).
			Name:       "unsafe-temp-file",
			SpeciesDir: filepath.Join(speciesRoot, "unsafe-temp-file"),
			RepoDir:    filepath.Join("testdata", "unsafe-temp-file", "repo"),
			GoldenPath: filepath.Join("testdata", "unsafe-temp-file", "fix.golden"),
			Fixer:      fixture.RecordedFixer(engine.FileDiff{Path: "repo.go", Patch: unsafeTempFilePatch}),
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

// longFunctionPatch (Sprint 019) extracts the product computation (p, q, r) out
// of Process into a `products(x, y, z)` helper, replacing the tail with a single
// call. Process drops from 7 statements to 4, and products is 3 statements — both
// below the threshold of 6 — so detector-clears matches zero. The result is
// identical (x*y + y*z), proven by repo_test.go. One hunk replaces old lines
// 13-17 (p/q/r/return/closing-brace) with the call, the brace, and the helper.
const longFunctionPatch = `--- a/repo.go
+++ b/repo.go
@@ -13,5 +13,9 @@
-	p := x * y
-	q := y * z
-	r := p + q
-	return r
-}
+	return products(x, y, z)
+}
+
+func products(x, y, z int) int {
+	p := x * y
+	q := y * z
+	return p + q
+}
`

// magicNumberPatch (Sprint 019) extracts the repeated literal 86400 (seconds per
// day) into a named constant secondsPerDay and replaces both occurrences. After
// the fix the multi-digit literal no longer appears in an expression (only the
// const's single definition), so the magic-number detector — which excludes the
// constant declaration site — matches zero. The computed value is unchanged,
// proven by repo_test.go. Three hunks: insert the const, then rewrite each use.
const magicNumberPatch = `--- a/repo.go
+++ b/repo.go
@@ -1,1 +1,3 @@
 package magicnum
+
+const secondsPerDay = 86400
@@ -8,1 +10,1 @@
-	return days * 86400
+	return days * secondsPerDay
@@ -12,1 +14,1 @@
-	return seconds / 86400
+	return seconds / secondsPerDay
`

// duplicateCodeSmallPatch (Sprint 019) extracts the small repeated normalize
// block (clamp into [0,100]) that appeared verbatim in ScoreA and ScoreB into a
// shared clamp helper, replacing both copies with a call. After the fix the
// duplicated block appears once (in clamp), so the duplicate-code detector —
// which requires the block in TWO functions — matches zero. Both callers compute
// the same result, proven by repo_test.go. Hunks rewrite each function body and
// append the helper.
const duplicateCodeSmallPatch = `--- a/repo.go
+++ b/repo.go
@@ -10,8 +10,1 @@
-	v := raw * 2
-	if v < 0 {
-		v = 0
-	}
-	if v > 100 {
-		v = 100
-	}
-	return v
+	return clamp(raw * 2)
@@ -21,8 +14,9 @@
-	v := raw + 10
-	if v < 0 {
-		v = 0
-	}
-	if v > 100 {
-		v = 100
-	}
-	return v
+	return clamp(raw + 10)
+}
+
+func clamp(v int) int {
+	if v < 0 {
+		v = 0
+	}
+	if v > 100 {
+		v = 100
+	}
+	return v
`

// deepNestingPatch (Sprint 019 signature) flattens Classify's depth-3 `if` nest
// into guard clauses: each outer condition is inverted and returns the
// fall-through value ("invalid") early, leaving the success path un-indented at
// the bottom. Behavior is identical on every input (the success path and each
// early-exit), proven by repo_test.go. One hunk replaces the nested body
// (old lines 10-17) with the flattened form; after the fix there is no depth-3
// nest, so detector-clears matches zero.
const deepNestingPatch = `--- a/repo.go
+++ b/repo.go
@@ -10,7 +10,13 @@
-	if ok {
-		if n > 0 {
-			if name != "" {
-				return "valid:" + name
-			}
-		}
-	}
-	return "invalid"
+	if !ok {
+		return "invalid"
+	}
+	if n <= 0 {
+		return "invalid"
+	}
+	if name == "" {
+		return "invalid"
+	}
+	return "valid:" + name
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

// hardcodedSecretConfigPatch (Sprint 021 P6 security) is the SOURCE half of the
// multi-file hardcoded-secret remediation: it removes the hardcoded AWS access
// key literal and reads the value from the AWS_ACCESS_KEY_ID environment variable
// instead, adding `import "os"`. After the fix no secret literal remains in
// config.go, so BOTH the command secret-scanner gate and detector-clears find
// nothing. Two hunks: insert the os import after the package clause, then rewrite
// the assignment.
const hardcodedSecretConfigPatch = `--- a/config.go
+++ b/config.go
@@ -11,1 +11,3 @@
 package hardcodedsecret
+
+import "os"
@@ -15,1 +17,1 @@
-	apiKey := "AKIAIOSFODNN7EXAMPLE"
+	apiKey := os.Getenv("AWS_ACCESS_KEY_ID")
`

// hardcodedSecretEnvPatch (Sprint 021 P6 security) is the CONFIG-EXAMPLE half of
// the remediation: it records the new variable in .env.example as NAME ONLY (no
// value), so deployment knows to supply AWS_ACCESS_KEY_ID at runtime while the
// example file never carries a real secret. One hunk appends the variable after
// the existing APP_NAME line.
const hardcodedSecretEnvPatch = `--- a/.env.example
+++ b/.env.example
@@ -5,1 +5,2 @@
 APP_NAME=hardcodedsecret-fixture
+AWS_ACCESS_KEY_ID=
`

// insecureRandomPatch (Sprint 021 P6 security) replaces the predictable math/rand
// token generation with crypto/rand: it swaps the "math/rand" import for
// "crypto/rand" + "encoding/hex" and rewrites the body to read crypto-strong
// random bytes (rand.Read) and hex-encode them. After the fix no math/rand call
// remains, so detector-clears matches zero; compile + tests:affected confirm the
// token is still the expected length. Two hunks: the import block and the body.
const insecureRandomPatch = `--- a/repo.go
+++ b/repo.go
@@ -7,4 +7,4 @@
 import (
-	"fmt"
-	"math/rand"
+	"crypto/rand"
+	"encoding/hex"
 )
@@ -13,6 +13,8 @@
 func SessionToken() string {
 	b := make([]byte, 16)
-	for i := range b {
-		b[i] = byte(rand.Intn(256))
-	}
-	return fmt.Sprintf("%x", b)
+	if _, err := rand.Read(b); err != nil {
+		panic(err)
+	}
+	return hex.EncodeToString(b)
 }
`

// unsafeTempFilePatch (Sprint 021 P6 security) replaces the predictable
// "/tmp/app-cache.tmp" path with os.CreateTemp, which picks an unpredictable name
// with 0600 perms and returns an open handle (closing the symlink/race/clobber
// window). It swaps the import set ("os" gains CreateTemp use; the hardcoded path
// constant is dropped) and rewrites the body. After the fix no hardcoded /tmp
// literal remains, so detector-clears matches zero; compile + tests:affected
// confirm a temp file is still created and written. Two hunks: import + body.
const unsafeTempFilePatch = `--- a/repo.go
+++ b/repo.go
@@ -13,7 +13,11 @@
 func WriteCache(data []byte) (string, error) {
-	path := "/tmp/app-cache.tmp"
-	if err := os.WriteFile(path, data, 0644); err != nil {
-		return "", err
-	}
-	return path, nil
+	f, err := os.CreateTemp("", "app-cache-*.tmp")
+	if err != nil {
+		return "", err
+	}
+	defer f.Close()
+	if _, err := f.Write(data); err != nil {
+		return "", err
+	}
+	return f.Name(), nil
 }
`

// TestTodoExpiredReportOnly drives the Sprint 019 report-only species through the
// detect-only harness: it asserts the species produces findings (the three seeded
// stale markers — a dated TODO, an issue-referenced FIXME, and a HACK) but
// produces NO diff and leaves the working tree byte-unchanged. This is the
// report-only acceptance criterion ("produces findings but no diff"). The species
// also ships DISABLED by default — that is asserted separately by
// TestTodoExpiredShipsDisabled (against the real embedded manifest). The bare
// `TODO:` note in the fixture is intentionally NOT counted, proving the rule only
// flags the staleness-signalling markers.
func TestTodoExpiredReportOnly(t *testing.T) {
	fixture.RunDetectOnlyCase(t, fixture.Case{
		Name:       "todo-expired",
		SpeciesDir: filepath.Join(speciesRoot, "todo-expired"),
		RepoDir:    filepath.Join("testdata", "todo-expired", "repo"),
	}, 3)
}

// TestReportOnlyFixtureSpecies proves the first-class report-only manifest kind
// (Sprint 022 Finding 4, fix.kind=none): a dedicated fixture species declared with
// NO [fix].transform and NO [verify].checks LOADS and VALIDATES through the real
// production loader/registry (RunDetectOnlyCase calls species.Load) and REPORTS its
// finding via the scout-equivalent detect-only path WITHOUT writing the working
// tree (the snapshot-before/after assertion). It deliberately drives no fixer — a
// report-only species proposes no change. This exercises the none-kind end to end
// independently of the todo-expired migration (TestTodoExpiredReportOnly covers the
// migrated built-in).
func TestReportOnlyFixtureSpecies(t *testing.T) {
	fixture.RunDetectOnlyCase(t, fixture.Case{
		Name:       "report-only-fixture",
		SpeciesDir: filepath.Join("testdata", "report-only-fixture", "species"),
		RepoDir:    filepath.Join("testdata", "report-only-fixture", "repo"),
	}, 1)
}

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
