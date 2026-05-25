---
name: ant-colony
description: Run the Ant code-cleanup colony (scout/fix/review/apply) and report findings, verified fixes, and skips. Use when the user asks to scan for code issues, run Ant, clean up unused imports / dead code / N+1 queries, or wants a CI-style code-health check.
argument-hint: "[scout|fix|review|apply] [path] [extra ant flags]"
allowed-tools: Bash(node *) Bash(ant *)
---

# Ant colony skill

A THIN front door over the `ant` binary. This skill runs `ant <verb> --json`
as a subprocess and parses its newline-delimited event stream into a readable
summary. It contains NO detection, fixing, trust, or orchestration logic — all
of that lives in the Go engine (TECHSPEC §3). The skill only execs the binary
and renders what the `--json` stream already says happened.

## Run

```!
node ${CLAUDE_SKILL_DIR}/src/run.ts $ARGUMENTS
```

## Instructions

The block above has already run `ant <verb> --json` and printed a structured
summary. Using only that output:

1. Lead with the headline: findings count, verified, skipped, applied, and the
   highest severity seen.
2. List each finding (species, file:line, severity, message).
3. For every SKIP, surface the failing verifier and its reason — a skip is a
   trust signal, never a hidden error. Do not downplay skips.
4. For verified/applied fixes, name the fixer (provenance) and the verifiers
   that passed.
5. If the run reported an error (operational failure such as a missing detector
   binary), report it plainly and stop.

Do not invent findings, re-run detection, or make fix/trust decisions — the
engine owns those. Render only what the stream contains.
