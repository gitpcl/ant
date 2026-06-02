// Package builtins embeds the v1 built-in species tree into the binary
// (TECHSPEC §2, §4). Built-in species folders are compiled in via go:embed so
// the binary ships with no external files; the engine discovers them from the
// embedded FS at startup (TECHSPEC §6.3). User/community species are layered on
// top from .ant/species/ by the species resolver, which reads built-ins through
// FS() below.
//
// The launch embedded species and their trust defaults are fixed by ADR-0002
// (docs/decisions/0002-launch-species.md): unused-import, dead-code (M2,
// deterministic, auto_apply=true), n+1-query, missing-await, nil-deref (M3, llm,
// auto_apply=false), and ai-slop (M4, llm, enabled=false). The Sprint 016
// species-cleanup wave adds further deterministic species (auto_apply=true):
// unused-variable (delete-match, incl. indented spans) and redundant-conversion
// (rewrite, old span → ast-grep fix: output).
//
// The Sprint 017 P2 orchestration wave adds tool-runner species that wrap an
// external formatter/autofixer (fix kind=tool): formatter-drift and import-sort
// (auto_apply=true, gated by formatter-idempotence + compile) and lint-autofix
// (auto_apply=true, gated by compile + tests:affected), plus trailing-debug-code
// (deterministic delete-match, propose-only).
//
// The Sprint 018 P3 bug-risk wave adds LLM-assisted, propose-only (auto_apply=
// false) species, each gated by compile + tests:affected + detector-clears with a
// recorded fixer response in CI: ignored-error (flagship — discarded `v, _ :=`
// error), unchecked-type-assertion (single-result `x.(T)`), resource-leak
// (signature — os.Open with no Close on any path), missing-context-timeout
// (context.Background() passed with no deadline), and unsafe-concurrency (premium
// — unsynchronized goroutine writing shared state). The security-stage member is
// sql-string-concat (SQL query built by string concatenation → bound parameter;
// its fix moves the interpolated value out of the SQL text to close the injection
// vector).
//
// The Sprint 019 P4 maintainability wave adds five propose-only refactor species
// (auto_apply=false; the verified-refactor review-UX showcase). Four are
// LLM-assisted (recorded fixer in CI), gated by compile + tests:affected +
// detector-clears: deep-nesting (SIGNATURE — depth-3 if nest to guard-clause
// flatten), long-function (body over the statement threshold to helper
// extraction), magic-number (repeated multi-digit literal to named constant), and
// duplicate-code-small (a small block duplicated across functions to a shared
// helper). Their thresholds/ignore-lists are encoded directly in each detect.yml
// as the documented species default (a manifest/ant.toml threshold parameter
// consumed by an ast-grep rule would require an engine change — out of scope; see
// .harness/progress_log.md Sprint-019 ENGINE-GAP #1). The fifth, todo-expired, is
// REPORT-ONLY and ships DISABLED by default (enabled=false): it flags stale
// TODO/FIXME/HACK markers via scout and proposes no fix / no diff.
//
// The Sprint 020 P5 dependency/config wave adds four propose-only (auto_apply=
// false) species that operate on NON-SOURCE files — manifests, lockfiles, and CI
// YAML — via the command-detector + command:-verifier escape hatches (TECHSPEC
// §4/§5): unused-dependency (SIGNATURE — go.mod requires cross-referenced against
// used imports; remove the unimported require, gated by a `go build`/`go vet`
// command: verifier + detector-clears), stale-dependency-pin (a duplicate/
// conflicting require; normalize, same Go build/vet gate), dead-config (a config
// key referenced nowhere in the tree; remove, gated by a config-parse command:
// verifier that keeps the file parseable). These three are deterministic
// delete-match removals/normalizations and ship enabled, propose-only;
// unused-dependency notes that high-confidence ecosystems could graduate to
// auto_apply later. duplicate-ci-step also uses a command detector but is
// REPORT-ONLY (fix.kind=none) and ships DISABLED: a `run:` step repeated across
// jobs usually cannot be safely auto-removed (jobs run on isolated runners, so the
// repeat is often required), so it reports the smell and proposes no change. Their
// fixtures are HERMETIC/offline (isolated zero-dep Go module; pure-stdlib parse
// scripts) — no network install.
//
// The Sprint 021 P6 security-hygiene wave adds three SECURITY-stage, propose-only
// (auto_apply=false) species whose edge is the VERIFIED REMEDIATION diff, not
// detection: hardcoded-secret (SIGNATURE — a command detector flags a high-entropy
// literal or known token shape, e.g. an AWS AKIA… key, in source; the llm fix
// removes the literal, reads it from os.Getenv, and records the variable in
// .env.example; gated by compile + a `command:` SECRET-SCANNER-CLEARS verifier
// that re-runs over the post-fix tree and must find NO secret — the remediation
// proof — plus detector-clears), insecure-random (an ast-grep detector flags
// math/rand used for a security value; the llm fix swaps in crypto/rand; gated by
// compile + tests:affected + detector-clears), and unsafe-temp-file (an ast-grep
// detector flags a predictable /tmp path; the llm fix switches to os.CreateTemp
// with OS-chosen unpredictable name + 0600 perms; same gate). Fixtures use ONLY
// obvious FAKE placeholders (the AWS-docs AKIAIOSFODNN7EXAMPLE) and a hermetic,
// self-contained scanner stub — CI depends on no installed secret scanner.
//
// The Sprint 023 P7 PHP/Laravel wave adds the first NON-Go-language species —
// seven engineer-stage species declaring languages=["php"], authored ONLY as
// species folders (ast-grep already parses PHP via `language: php`; the
// tool-runner, formatter-idempotence, command detector, and command:verify.sh
// escape hatches all already exist, so no engine change). Two are tool-runner
// orchestration species (auto_apply=true, gated by formatter-idempotence ONLY):
// pint-format (Laravel Pint) and php-cs-fixer (ships DISABLED — overlaps Pint).
// One is deterministic delete-match, propose-only: laravel-dd-dump-debug
// (statement-level dd/dump/ray). Three are LLM-assisted, propose-only:
// laravel-env-call (env() outside config/ -> config('x.y')), laravel-n+1-eager-load
// (relation access in a foreach -> ->with(...)), and livewire-public-untyped-prop
// (untyped public Livewire prop -> typed + #[Locked]). One is a command detector,
// propose-only: laravel-orphan-config-key (a config/*.php key referenced nowhere
// via config('x.y') — a near-exact mirror of dead-config). CRITICAL CONTRACT: no
// PHP species lists `compile` or `tests:affected` — on a non-Go repo the hardcoded
// `go build ./...` gate is a vacuous pass, so the trust proof is detector-clears /
// formatter-idempotence / a `command:verify.sh` running `php -l`, each fixture
// RequiredTools=["php"]-gated so CI without PHP skips green. The two SECURITY-stage
// PHP species — laravel-mass-assignment (Eloquent mass-assignment from
// $request->all() -> $request->validated()/whitelist) and laravel-raw-where-concat
// (raw SQL built by concatenation in whereRaw/DB::raw -> bound parameters) — are
// LLM-assisted, propose-only (auto_apply=false), gated by detector-clears + a
// php -l command:verify.sh (no compile/tests:affected), and authored by the
// security stage to the same contract.
//
// The Sprint 024 P8 Python/FastAPI wave adds the second non-Go-language family —
// eight engineer-stage species declaring languages=["py"] (the SAME token the
// pre-existing lint-autofix uses — no new spelling), authored ONLY as species
// folders (ast-grep already parses Python via `language: python`; the tool-runner,
// formatter-idempotence, command detector, and command:verify.sh escape hatches
// all already exist, so no engine change). Four are tool-runner orchestration
// species (auto_apply=true, gated by formatter-idempotence): ruff-format and
// isort-imports (ruff's `ruff format` / `ruff check --select I --fix`) ship
// ENABLED; black-format ships DISABLED (it overlaps ruff-format — a project enables
// one or the other); ruff-autofix (the safe `ruff check --fix` subset) adds a
// `command:verify.sh` py_compile gate alongside formatter-idempotence. One is a
// deterministic delete-match, propose-only: python-debug-print (statement-level
// print/breakpoint/pdb.set_trace), gated by detector-clears + py_compile. Three are
// LLM-assisted, propose-only: fastapi-sync-route-blocking (a plain `def` under an
// @app/@router route decorator -> async/offload), fastapi-depends-default-arg (a
// mutable default `= []`/`= {}` -> `= None` + in-body init), and python-bare-except
// (`except:` / `except Exception: pass` -> narrowed + handled). CRITICAL CONTRACT
// (same as Sprint 023): no Python species lists `compile` or `tests:affected` — on
// a non-Go repo the hardcoded `go build ./...` gate is a vacuous pass, so the trust
// proof is detector-clears / formatter-idempotence / a `command:verify.sh` running
// `python -m py_compile`, each fixture RequiredTools-gated (`python3`, plus the tool
// for the enabled formatter species) so CI without the toolchain skips green. The
// two SECURITY-stage Python species are authored separately by the security
// stage to the same contract: python-sql-fstring (SQL built by an f-string
// interpolated into cur.execute()/text() -> a bound parameter; an ast-grep
// `language: python` detector requiring a `string` with an `interpolation` child,
// so a plain literal and an already-parameterized `execute(sql, params)` call are
// NOT flagged) and fastapi-hardcoded-secret (a string literal assigned to a
// credential-named target SECRET_KEY/API_KEY/PASSWORD -> os.environ[...]/os.getenv
// + a recorded .env.example entry, a multi-file llm fix; the right-side `string`
// constraint leaves an env-backed `= os.getenv(...)` value and a non-secret-named
// target NOT flagged). Both are auto_apply=false, gated by detector-clears + a
// `python -m py_compile` command:verify.sh (no compile/tests:affected), each
// fixture RequiredTools=["python3"]-gated; py_compile only byte-compiles and never
// EXECUTES the post-diff repo code, so the verifier is execution-free.
package builtins

import "embed"

// files embeds every built-in species folder. Each pattern names a species
// directory so a stray top-level file (this .go source, a README) is never
// embedded — only the species manifests and their referenced rule/prompt files.
// embed.FS paths are always slash-separated and rooted at this directory, so the
// resolver sees "unused-import/species.toml", etc.
//
//go:embed unused-import dead-code unused-variable redundant-conversion unreachable-code empty-block duplicate-condition redundant-nil-check ineffective-assignment formatter-drift import-sort lint-autofix trailing-debug-code n+1-query missing-await nil-deref ai-slop ignored-error unchecked-type-assertion resource-leak missing-context-timeout unsafe-concurrency sql-string-concat deep-nesting long-function magic-number duplicate-code-small todo-expired unused-dependency dead-config duplicate-ci-step stale-dependency-pin hardcoded-secret insecure-random unsafe-temp-file pint-format php-cs-fixer laravel-dd-dump-debug laravel-env-call laravel-n+1-eager-load livewire-public-untyped-prop laravel-orphan-config-key laravel-mass-assignment laravel-raw-where-concat ruff-format ruff-autofix black-format isort-imports python-debug-print fastapi-sync-route-blocking fastapi-depends-default-arg python-bare-except python-sql-fstring fastapi-hardcoded-secret
var files embed.FS

// FS returns the embedded built-in species tree as a read-only fs.FS-compatible
// embed.FS. The species resolver passes this to the loader exactly as it passes
// an os.DirFS for the on-disk user tree, so built-in and user species share one
// load+validate path (loader.Load). Returning the concrete embed.FS (rather than
// fs.FS) keeps the zero-allocation embed access while still satisfying fs.FS at
// call sites.
func FS() embed.FS { return files }
