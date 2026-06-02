#!/bin/sh
# vue-v-html-xss verifier (Sprint 025 JS/TS + Vue wave, SECURITY stage; command:
# escape hatch). The remediation-proof gate for the .vue v-html XSS fix.
#
# Runs in the SCRATCH COPY (verify/command.go applies the proposed diff first), so
# it proves the post-fix SFC is (a) free of the raw v-html sink and (b) still
# well-formed — in place of the (vacuous on a Vue/TS repo) Go-build compile check
# the sprint-025 contract forbids.
#
# Two gates, in priority order — BOTH are execution-free (the wave-wide "never run
# repo code at verify time" rule; the core security property of this species):
#
#   1. GREP-CLEARS (always): re-scan every *.vue <template> region for a surviving
#      `v-html` binding. If ANY remains the fix did not remediate the XSS sink, so
#      the gate FAILS and the fix is skipped (never staged). This is the security
#      remediation proof and needs no toolchain, so it runs even on a node-less CI.
#   2. vue-tsc --noEmit (when present): a full SFC TYPE-CHECK proving the
#      sanitizer/v-text replacement still type-checks. `vue-tsc --noEmit` only
#      type-checks; it NEVER executes the SFC. If vue-tsc is absent the grep-clears
#      gate alone stands (the contract's grep-clears fallback).
#
# RequiredTools=["node"] in the fixture skips this case when node is absent; the
# grep-clears step itself is pure POSIX sh + python3 so it always runs where the
# case runs. We deliberately do NOT extract-and-tsc the <script> here (vue-reactivity
# -misuse does that for a script-level smell) — this smell and its fix live in the
# TEMPLATE, where grep-clears is the precise, hermetic remediation proof.
set -eu

# --- Gate 1: grep-clears — no v-html survives in any template (security proof) ---
remaining=$(python3 - <<'PY'
import os, re
tmpl_open = re.compile(r'<template\b', re.IGNORECASE)
tmpl_close = re.compile(r'</template>', re.IGNORECASE)
vhtml = re.compile(r'(?:(?<![\w:-])v-html\s*=|(?<![\w-])v-bind:html\s*=|(?<![\w-]):html\s*=)')
count = 0
for dp, dn, fns in os.walk("."):
    if "node_modules" in dp.split(os.sep):
        continue
    for fn in sorted(fns):
        if not fn.endswith(".vue"):
            continue
        try:
            lines = open(os.path.join(dp, fn), encoding="utf-8").read().splitlines()
        except OSError:
            continue
        ins = False
        for ln in lines:
            if not ins and tmpl_open.search(ln):
                ins = True
            if ins and vhtml.search(ln):
                count += 1
            if ins and tmpl_close.search(ln):
                ins = False
print(count)
PY
)
if [ "$remaining" -ne 0 ]; then
	echo "v-html binding still present in $remaining template line(s) — XSS not remediated" >&2
	exit 1
fi
echo "grep-clears: no v-html binding remains in any .vue template"

# --- Gate 2: vue-tsc --noEmit when present (full SFC type-check, no execution) ---
if command -v vue-tsc >/dev/null 2>&1; then
	found=0
	for f in $(find . -name '*.vue' -type f -not -path './node_modules/*' | sort); do
		found=1
		vue-tsc --noEmit --skipLibCheck "$f" || exit 1
	done
	[ "$found" -eq 0 ] && echo "no .vue files to type-check"
	exit 0
fi

echo "vue-tsc not present — grep-clears gate stands as the remediation proof"
