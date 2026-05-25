# 🐜 Ant

**Autonomous code-cleanup for your repo — a colony of ants that detect, fix, and *verify* code smells in parallel, and never touch your working tree until you say so.**

Ant is a single-binary CLI. Point it at a repo and it dispatches a colony of workers ("ants"), each of which picks up one finding, proposes a fix, and **runs that fix through a verifier gate** (does it still compile? do the affected tests pass? is the smell actually gone?). Only verified fixes are staged for your review. Nothing is written to your working tree until you explicitly apply it — on a new branch, by default.

The core idea: **detection is allowed to be imperfect because verification is strict.** A fix that breaks the build is silently skipped, and you're told why. A skip is a trust signal, not a hidden error.

> Status: **v1 feature-complete.** MIT licensed. Pure-Go, CGO-free single binary — runs anywhere `go` does, including arm64 (Raspberry Pi / Jetson / Apple silicon).

---

## Quick start

```bash
# Install (see Installation for alternatives)
curl -fsSL https://raw.githubusercontent.com/gitpcl/ant/main/install.sh | sh

# 1. Scout — detect and report, change nothing
ant                      # bare `ant` == `ant scout` with a summary
ant scout ./path --detail

# 2. Fix — produce verified diffs into a staging area (working tree untouched)
ant fix

# 3. Review — walk the staged diffs: accept / skip / diff / explain / next / quit
ant review

# 4. Apply — land the accepted diffs, on a new branch by default
ant apply
```

> **Prerequisite:** the built-in species detect via [`ast-grep`](https://ast-grep.github.io/), so it must be on your `PATH`. Without it, `scout`/`fix` exit cleanly with code 2 and a clear message. See [docs/guide/install.md](docs/guide/install.md).

---

## The git-shaped loop

Ant follows a **stage-then-apply** model — the same shape as `git add` → `git commit`:

| Command | What it does | Touches your code? |
| --- | --- | --- |
| `ant` / `ant scout` | Run detectors, report findings | **No** |
| `ant fix` | Detect → fix → verify → **stage** verified diffs | No (only `--apply` does) |
| `ant review` | Walk staged diffs with full provenance | No |
| `ant apply` | Land **accepted** diffs (new branch by default; `--no-branch` for current) | **Yes** |
| `ant init` | Scaffold an `ant.toml` | Writes config |
| `ant species list / install / remove` | Manage detection species | No / clones into `.ant/species/` / No |

Global flags: `--json`, `--fail-on=<low\|medium\|high>`, `--config`, `--fixer`, `--model`, `--concurrency`.

---

## Built-in species

A **species** bundles a detector + a fix strategy + a verifier set + a trust default. Trust is granted **per species — never globally.**

| Species | Fix strategy | Default | Notes |
| --- | --- | --- | --- |
| `unused-import` | deterministic | **auto-apply** | mechanical; the compile gate makes a wrong removal impossible |
| `dead-code` | deterministic | **auto-apply** | annotation-driven removal, gated by compile |
| `n+1-query` | LLM-assisted | propose-only | model fix, always staged for review |
| `missing-await` | LLM-assisted | propose-only | un-awaited goroutine / missing synchronization |
| `nil-deref` | LLM-assisted | propose-only | guards a likely nil dereference |
| `ai-slop` | LLM-assisted | **disabled** | fuzzy classifier; too noisy to enable by default — opt-in only |

Deterministic species can auto-apply because a wrong fix won't compile. LLM species default to **propose-only** — verified, but always staged for a human. `ai-slop` ships **off**. See [ADR 0002](docs/decisions/0002-launch-species.md).

---

## The trust model

Trust is the heart of the product (and the reason you can let it touch your code):

- **Verified or skipped, never silent.** Every proposed fix runs the verifier gate: `diff-bounded` → `compile` → `detector-clears` → `tests:affected`. Fail any required check → discarded and surfaced as a skip with the reason.
- **Per-species trust, no global switch.** `ant.toml` overrides a species' default; there is no "trust everything" flag.
- **Freshly-installed species are forced propose-only** until you've reviewed them once — *regardless* of what their manifest claims. Installing a community species can never auto-apply on first run.
- **`ant species install` runs no repo code.** It clones, validates structure, and copies only well-formed species folders. A repo's `verify.sh` / `Makefile` / `go:generate` never executes at install time.

---

## CI mode

`ant scout` is a composable CI gate:

```bash
ant scout --json --fail-on=high
# exit 0 = nothing at/above threshold · 1 = threshold tripped · 2 = operational error
```

`--json` emits a stable, golden-tested event stream (`run.start` … `run.end`) that the front-doors and your own tooling can parse. See [docs/guide/ci.md](docs/guide/ci.md).

---

## Installation

```bash
# curl | sh — detects OS/arch, verifies the checksum, aborts on mismatch
curl -fsSL https://raw.githubusercontent.com/gitpcl/ant/main/install.sh | sh

# go install
go install github.com/gitpcl/ant/cmd/ant@latest

# or download a prebuilt static binary from Releases
```

Prebuilt targets: linux `amd64`/`arm64`, darwin `amd64`/`arm64`, windows `amd64`. The arm64 builds make Ant a first-class citizen on Raspberry Pi / Jetson. Full matrix in [docs/guide/install.md](docs/guide/install.md).

---

## How it works

```
cmd/ant/  (thin CLI: parse flags, call the engine, render)
   │
   ▼
internal/engine/  (the library — all logic lives here)
   colony/    scheduler + worker pool + the run loop (+ optional trails)
   detect/    Detector adapters → shells out to ast-grep (a plugin boundary)
   fix/       Fixer adapters: deterministic · rawmodel · claudecode · codex · pi
   verify/    diff-bounded · compile · detector-clears · tests:affected
   species/   manifest model · registry · go:embed built-ins · trust model
   stage/ · store/ · events/ · config/ · telemetry/
```

- **Detection is a plugin boundary**, not a build dependency — Ant shells out to `ast-grep` (the default), with `semgrep`/`eslint`/`command` available per species. The detector is chosen **per species**, matching the confidence a species needs ([ADR 0004](docs/decisions/0004-detector-strategy-and-confidence-tiers.md)).
- **Fixers are pluggable**: deterministic transforms (no model), a provider-agnostic OpenAI-compatible `rawmodel`, and harness adapters that drive Claude Code / Codex / Pi one-shot. The model is always configured, never hardcoded.
- **`tests:affected`** runs only the tests impacted by a diff (coverage-map → import-graph → package-fallback) and reports *which* strategy it used — never silently running the whole suite.
- **Front-doors** (`adapters/`): thin TypeScript shells (Claude Code skill, Pi extension) that exec the binary and parse `--json`.
- **Telemetry** is **opt-in and off by default** — when enabled it sends only privacy-safe aggregates (species usage, accept rate, verifier catch rate), never code, paths, or diffs.

The hard architectural rule: **all logic lives in `internal/engine`; `cmd/ant` only parses and renders** — enforced by a boundary test.

---

## Configuration (`ant.toml`)

Zero-config by default. `ant init` scaffolds a commented file:

```toml
[colony]
fixer = "pi"               # pi | claudecode | codex | rawmodel | deterministic
model = "qwen2.5-coder"

[ignore]
paths = ["vendor/", "node_modules/"]

[species.unused-import]
auto_apply = true

[species.ai-slop]
enabled = true             # opt into the disabled-by-default fuzzy species
```

---

## Documentation

- **[Quickstart](docs/guide/quickstart.md)** — the full scout → fix → review → apply loop
- **[Install](docs/guide/install.md)** — every install method + the OS/arch matrix
- **[CI mode](docs/guide/ci.md)** — `--fail-on`, exit codes, the `--json` stream, a GitHub Actions snippet
- **[Species authoring](docs/guide/species-authoring.md)** — write and publish your own species
- **Design decisions** — [ADRs](docs/decisions/): command model · launch species · license & trails · detector strategy & confidence tiers
- **[PRD](docs/PRD.md)** · **[Technical spec](docs/TECHSPEC.md)**

---

## Contributing

The fastest way to extend Ant is to **author a species** — no Go required, just a `species.toml` + an `ast-grep` rule (and a fix prompt for LLM species). See the [species-authoring guide](docs/guide/species-authoring.md). Community species install via `ant species install <git-url>` and are propose-only until you've reviewed them once.

## License

[MIT](LICENSE) © Pedro Lopes
