# Capability Matrix

This matrix lists every **built-in species** and what running it requires, so an
operator can tell at a glance which species depend on an AST matcher, run a
command/script, need an external tool on `PATH`, reach an LLM/network endpoint,
or only report (propose no fix).

The table is **generated** from the authoritative capability metadata each
species declares (or that the loader infers) — `requires_exec`,
`requires_network`, `requires_tool`, `report_only` — exposed through
`species.Resolved.Capabilities()`. It is the same authority `ant doctor` and
`ant species validate` read. A doc-consistency test
(`internal/engine/capmatrix/capmatrix_test.go`) re-renders the matrix from the
embedded species and fails if this table drifts from the metadata, so the doc
can never silently rot. Regenerate it intentionally with:

```sh
UPDATE_DOCS=1 go test ./internal/engine/capmatrix/
```

## Columns

| Column | Sourced from | Meaning |
| --- | --- | --- |
| **ast-grep** | `requires_tool == "ast-grep"` | The species detects with the ast-grep AST matcher (needs the `ast-grep` binary on `PATH`). |
| **command/script** | `requires_exec` | The species execs a process during scan/fix — a `command` detector script or a tool-runner fix. |
| **external tool** | `requires_tool` | The named external binary the species needs on `PATH` (`ast-grep`, `gofmt`, `goimports`, `ruff`, …). |
| **LLM / network** | `requires_network` | The species reaches the network — an `llm` fix calls a model endpoint. |
| **report-only** | `report_only` | The species reports findings but proposes no change (`fix.kind = "none"`); `ant scout` reports it, `ant fix` rejects it. |

A `yes` cell means the capability applies; `-` means it does not.

## Built-in species

<!-- BEGIN GENERATED CAPABILITY MATRIX -->
| Species | ast-grep | command/script | external tool | LLM / network | report-only |
| --- | --- | --- | --- | --- | --- |
| `ai-slop` | yes | - | ast-grep | yes | - |
| `black-format` | - | yes | black | - | - |
| `dead-code` | yes | - | ast-grep | - | - |
| `dead-config` | - | yes | - | - | - |
| `deep-nesting` | yes | - | ast-grep | yes | - |
| `duplicate-ci-step` | - | yes | - | - | yes |
| `duplicate-code-small` | yes | - | ast-grep | yes | - |
| `duplicate-condition` | yes | - | ast-grep | - | - |
| `empty-block` | yes | - | ast-grep | - | - |
| `eslint-autofix` | - | yes | eslint | - | - |
| `fastapi-depends-default-arg` | yes | - | ast-grep | yes | - |
| `fastapi-hardcoded-secret` | yes | - | ast-grep | yes | - |
| `fastapi-sync-route-blocking` | yes | - | ast-grep | yes | - |
| `formatter-drift` | - | yes | gofmt | - | - |
| `hardcoded-secret` | - | yes | - | yes | - |
| `ignored-error` | yes | - | ast-grep | yes | - |
| `import-sort` | - | yes | goimports | - | - |
| `ineffective-assignment` | yes | - | ast-grep | - | - |
| `inertia-raw-response` | yes | - | ast-grep | yes | - |
| `insecure-random` | yes | - | ast-grep | yes | - |
| `isort-imports` | - | yes | ruff | - | - |
| `js-console-debug` | yes | - | ast-grep | - | - |
| `js-eqeqeq` | yes | - | ast-grep | - | - |
| `laravel-dd-dump-debug` | yes | - | ast-grep | - | - |
| `laravel-env-call` | yes | - | ast-grep | yes | - |
| `laravel-mass-assignment` | yes | - | ast-grep | yes | - |
| `laravel-n+1-eager-load` | yes | - | ast-grep | yes | - |
| `laravel-orphan-config-key` | - | yes | - | - | - |
| `laravel-raw-where-concat` | yes | - | ast-grep | yes | - |
| `lint-autofix` | - | yes | ruff | - | - |
| `livewire-public-untyped-prop` | yes | - | ast-grep | yes | - |
| `long-function` | yes | - | ast-grep | yes | - |
| `magic-number` | yes | - | ast-grep | yes | - |
| `missing-await` | yes | - | ast-grep | yes | - |
| `missing-context-timeout` | yes | - | ast-grep | yes | - |
| `n+1-query` | yes | - | ast-grep | yes | - |
| `nil-deref` | yes | - | ast-grep | yes | - |
| `php-cs-fixer` | - | yes | php-cs-fixer | - | - |
| `pint-format` | - | yes | pint | - | - |
| `prettier-format` | - | yes | prettier | - | - |
| `python-bare-except` | yes | - | ast-grep | yes | - |
| `python-debug-print` | yes | - | ast-grep | - | - |
| `python-sql-fstring` | yes | - | ast-grep | yes | - |
| `redundant-conversion` | yes | - | ast-grep | - | - |
| `redundant-else` | yes | - | ast-grep | - | - |
| `redundant-nil-check` | yes | - | ast-grep | - | - |
| `resource-leak` | yes | - | ast-grep | yes | - |
| `ruff-autofix` | - | yes | ruff | - | - |
| `ruff-format` | - | yes | ruff | - | - |
| `sql-string-concat` | yes | - | ast-grep | yes | - |
| `stale-dependency-pin` | - | yes | - | - | - |
| `todo-expired` | yes | - | ast-grep | - | yes |
| `trailing-debug-code` | yes | - | ast-grep | - | - |
| `ts-no-explicit-any` | yes | - | ast-grep | yes | - |
| `unchecked-type-assertion` | yes | - | ast-grep | yes | - |
| `unreachable-code` | yes | - | ast-grep | - | - |
| `unsafe-concurrency` | yes | - | ast-grep | yes | - |
| `unsafe-temp-file` | yes | - | ast-grep | yes | - |
| `unused-dependency` | - | yes | - | - | - |
| `unused-import` | yes | - | ast-grep | - | - |
| `unused-variable` | yes | - | ast-grep | - | - |
| `vue-reactivity-misuse` | - | yes | - | yes | - |
| `vue-v-html-xss` | - | yes | - | yes | - |
<!-- END GENERATED CAPABILITY MATRIX -->

## Per-language verifier gate matrix

The built-in `compile` and `tests:affected` verifiers are **per-language**
(Sprint 026). They resolve a diff's language from file extension via the single
`internal/engine/langmap` authority — the same map that scopes detection — and
dispatch to the matching checker below. A language with no row is an **honest
skip with a reason**, never a vacuous pass; a supported language whose toolchain
binary is absent is a **clean skip** (CI without that toolchain stays green).

| Language | `compile` | `tests:affected` |
|---|---|---|
| Go | `go build ./...` | `go test` (coverage-map → import-graph → package-fallback) |
| TypeScript (`.ts`/`.tsx`) | `tsc --noEmit` | `vitest run` (co-located `*.test.ts`/`*.spec.ts`) |
| JavaScript (`.js`/`.jsx`) | — (typecheck via TS) | `vitest run` (co-located `*.test.js`/`*.spec.js`) |
| Vue (`.vue`) | `vue-tsc --noEmit` | — |
| PHP (`.php`) | `php -l` per changed file | `phpunit` (co-located `*Test.php`) |
| Python (`.py`) | `python -m py_compile` | `pytest` (co-located `test_*.py`) |
| any other | honest skip: `no compile checker for <lang>` | honest skip: `no test runner for <lang>` |

Adding a language is one row in `langmap` plus one builder/runner entry in the
`compile` `BuildTable` and the `tests:affected` runner table — no engine changes.
