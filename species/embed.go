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
package builtins

import "embed"

// files embeds every built-in species folder. Each pattern names a species
// directory so a stray top-level file (this .go source, a README) is never
// embedded — only the species manifests and their referenced rule/prompt files.
// embed.FS paths are always slash-separated and rooted at this directory, so the
// resolver sees "unused-import/species.toml", etc.
//
//go:embed unused-import dead-code unused-variable redundant-conversion unreachable-code empty-block duplicate-condition redundant-nil-check ineffective-assignment formatter-drift import-sort lint-autofix trailing-debug-code n+1-query missing-await nil-deref ai-slop ignored-error unchecked-type-assertion resource-leak missing-context-timeout unsafe-concurrency sql-string-concat deep-nesting long-function magic-number duplicate-code-small todo-expired
var files embed.FS

// FS returns the embedded built-in species tree as a read-only fs.FS-compatible
// embed.FS. The species resolver passes this to the loader exactly as it passes
// an os.DirFS for the on-disk user tree, so built-in and user species share one
// load+validate path (loader.Load). Returning the concrete embed.FS (rather than
// fs.FS) keeps the zero-allocation embed access while still satisfying fs.FS at
// call sites.
func FS() embed.FS { return files }
