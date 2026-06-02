#!/bin/sh
# vue-v-html-xss detector (Sprint 025 JS/TS + Vue wave, SECURITY stage; command
# escape hatch). Templated on species/dead-config; the .vue handling mirrors the
# line-preserving, NON-EXECUTING pattern species/vue-reactivity-misuse established.
#
# ===========================================================================
# .vue TEMPLATE-SCAN STRATEGY (the documented Vue route — no engine change)
# ===========================================================================
# `.vue` Single-File Components are NOT a first-class ast-grep language. The smell
# here — a `v-html` binding in the template — is a FLAT, line-local pattern, so
# (unlike vue-reactivity-misuse, which extracts <script setup> and runs ast-grep)
# this detector is a line-preserving REGEX over the <template> region:
#
#   1. For each *.vue file, walk its lines tracking whether we are inside the
#      `<template> ... </template>` region (the only place a directive binding is
#      meaningful). Lines outside the template are ignored — a `v-html` mentioned in
#      <script> or a comment is not a render-time XSS sink.
#   2. Within the template, match a `v-html` directive binding: `v-html=`,
#      `:v-html=`, `v-html =`, or a `v-bind:` of it. `v-html` renders its bound
#      expression as RAW, UNSANITIZED HTML — if any part of the value is
#      attacker-influenced this is a stored/reflected XSS sink (Vue's own docs flag
#      it). The fix sanitizes the value or switches to `v-text`/`{{ }}` interpolation
#      (which HTML-escapes).
#   3. Emit one finding per matching template line with `file` = the real .vue path
#      and `line` = the 1-based line in the original .vue (the walk preserves line
#      positions 1:1, so no offset arithmetic), `sourceLine` = the verbatim line so
#      the deterministic diff-bounded check and the LLM fix patch the real file.
#
# The detector ONLY reads and MATCHES text — it never executes the SFC, imports it,
# evaluates the bound expression, or runs any repo code (the wave-wide "never run
# repo code at scan time" rule; the core security property of this species). It is a
# hermetic POSIX-sh + sed/grep scan, no network.
#
# Contract (detect/command.go): argv = <interpreter> <script> <scope-root>.
# Output: JSON array of findings on stdout ("[]" = none). Deterministic: file +
# line order. No external tool required for detection (the grep is built-in); the
# verify gate is the one that wants node/vue-tsc.
set -eu

root="${1:-.}"

ROOT="$root" python3 - <<'PY'
import json, os, re, sys

root = os.environ["ROOT"]

# Template region boundaries (a top-level <template ...> ... </template>). We only
# look for v-html INSIDE the template — a mention elsewhere is not a render sink.
tmpl_open = re.compile(r'<template\b', re.IGNORECASE)
tmpl_close = re.compile(r'</template>', re.IGNORECASE)

# A v-html directive binding in any of its spellings: `v-html=`, `v-html =`,
# `:v-html=`, or `v-bind:v-html` (rare but possible). The leading boundary keeps
# `data-v-html`-style custom attrs from matching. We match the directive name only;
# the bound value is never evaluated.
vhtml = re.compile(r'(?:(?<![\w:-])v-html\s*=|(?<![\w-])v-bind:html\s*=|(?<![\w-]):html\s*=)')

findings = []

vue_files = []
for dirpath, dirnames, filenames in os.walk(root):
    if "node_modules" in dirpath.split(os.sep):
        continue
    for fn in filenames:
        if fn.endswith(".vue"):
            vue_files.append(os.path.join(dirpath, fn))
vue_files.sort()

for vue in vue_files:
    try:
        with open(vue, "r", encoding="utf-8") as fh:
            lines = fh.read().splitlines()
    except OSError:
        continue

    rel = os.path.relpath(vue, root)
    in_template = False
    for i, ln in enumerate(lines):
        # Update region membership. A single line can both open and close, but the
        # common case is open-on-one-line; we treat the opening line as inside.
        if not in_template and tmpl_open.search(ln):
            in_template = True
            # fall through: a v-html on the same line as <template> still counts
        if in_template and vhtml.search(ln):
            findings.append({
                "file": rel,
                "line": i + 1,
                "severity": "high",
                "message": "v-html renders unsanitized HTML (XSS risk); sanitize the value or use v-text / {{ }} interpolation",
                "snippet": ln,
                "sourceLine": ln,
                "ruleId": "vue-v-html-xss",
            })
        if in_template and tmpl_close.search(ln):
            in_template = False

json.dump(findings, sys.stdout)
sys.stdout.write("\n")
PY
