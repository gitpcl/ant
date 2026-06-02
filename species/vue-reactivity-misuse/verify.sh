#!/bin/sh
# vue-reactivity-misuse verifier (Sprint 025 JS/TS + Vue wave, command: escape
# hatch). The type-check gate for the .vue reactivity fix.
#
# Runs in the SCRATCH COPY (verify/command.go applies the proposed diff first), so
# this proves the post-fix <script setup> still TYPE-CHECKS — the type-check gate
# the sprint-025 contract requires in place of the (vacuous on a Vue/TS repo)
# Go-build compile check.
#
# `.vue` is not a first-class tsc input, so — mirroring the detector's extraction
# strategy — this verifier EXTRACTS each *.vue `<script setup>` block to a temp
# `.ts` (line-preserving) and runs `tsc --noEmit` over the extracted scripts. It
# prefers `vue-tsc` when present (full SFC type-check); otherwise it falls back to
# extracting the script and running plain `tsc`. It NEVER executes the SFC (the
# wave-wide "never run repo code at verify time" rule) — the temp file is a blanked
# copy, only type-checked.
#
# RequiredTools=["node","tsc"] in the fixture skips this case when the toolchain is
# absent (vue-tsc is optional), exactly as the ast-grep species skip without the
# matcher — so CI without the toolchain stays green while the gate runs where
# present.
set -eu

# Prefer a real vue-tsc if the project has one (full SFC type-check).
if command -v vue-tsc >/dev/null 2>&1; then
	found=0
	for f in $(find . -name '*.vue' -type f -not -path './node_modules/*' | sort); do
		found=1
		vue-tsc --noEmit --skipLibCheck "$f" || exit 1
	done
	[ "$found" -eq 0 ] && echo "no .vue files to type-check"
	exit 0
fi

# Fallback: extract each <script setup> to a line-preserving temp .ts and tsc it.
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

n=0
for vue in $(find . -name '*.vue' -type f -not -path './node_modules/*' | sort); do
	out="$tmpdir/script_$n.ts"
	n=$((n + 1))
	VUE="$vue" OUT="$out" python3 - <<'PY'
import os, re
vue = os.environ["VUE"]
out = os.environ["OUT"]
op = re.compile(r'^\s*<script\b[^>]*\bsetup\b')
cl = re.compile(r'^\s*</script>')
# A bare `import { ... } from "vue"` cannot resolve in a standalone tsc run (no
# node_modules in the scratch tree), so the extracted script BLANKS vue imports and
# declares the reactivity helpers as ambient `any`-returning functions instead. The
# gate then type-checks the SCRIPT BODY (the thing the fix changed) hermetically,
# without depending on @vue/* type packages — it is a parse/shape check, not a full
# SFC type-check (that is vue-tsc's job when present).
vue_import = re.compile(r'^\s*import\s.*\bfrom\s+["\']vue["\']\s*;?\s*$')
lines = open(vue, encoding="utf-8").read().splitlines()
res = []
ins = False
for ln in lines:
    if not ins and op.match(ln):
        ins = True; res.append(""); continue
    if ins and cl.match(ln):
        ins = False; res.append(""); continue
    if ins and vue_import.match(ln):
        res.append("")  # blank the vue import (declared ambient below)
        continue
    res.append(ln if ins else "")
shim = "declare function reactive(o: any): any; declare function toRefs(o: any): any; declare function ref(v?: any): any;"
open(out, "w", encoding="utf-8").write(shim + "\n" + "\n".join(res) + "\n")
PY
done

if [ "$n" -eq 0 ]; then
	echo "no .vue files to type-check"
	exit 0
fi

# Type-check each extracted script in its OWN tsc invocation: the scripts share no
# module scope, so a single multi-file run would falsely report "cannot redeclare"
# for top-level names common across components. skipLibCheck + a permissive target
# keep the check at the script-body level (the vue import is blanked + shimmed
# above, so no @vue/* types are needed).
for ts in "$tmpdir"/*.ts; do
	tsc --noEmit --allowJs --skipLibCheck --moduleResolution node --target es2020 "$ts" || exit 1
done
