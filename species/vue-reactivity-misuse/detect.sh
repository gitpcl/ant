#!/bin/sh
# vue-reactivity-misuse detector (Sprint 025 JS/TS + Vue wave, command escape
# hatch). Templated on species/dead-config + n+1-query.
#
# ===========================================================================
# .vue EXTRACTION STRATEGY (the documented Vue route — no engine change)
# ===========================================================================
# `.vue` Single-File Components are NOT a first-class ast-grep language, so this
# detector covers them WITHOUT a parser shim:
#
#   1. For each *.vue file, locate the `<script setup ...>` ... `</script>` block.
#   2. Write a TEMP `.ts` file that is the SAME length as the .vue, with every
#      line OUTSIDE the script block BLANKED (replaced by an empty line) and the
#      script lines copied verbatim. Because the temp file preserves line
#      positions 1:1, an ast-grep match at temp line N maps DIRECTLY back to line
#      N of the original .vue — no offset arithmetic, no off-by-one.
#   3. Run `ast-grep` over the temp .ts for the reactivity smell: destructuring a
#      `reactive()` object — `const { ... } = state` where `state` was bound by
#      `const state = reactive(...)`. Destructuring a reactive proxy copies the
#      CURRENT primitive values out, severing reactivity (the canonical Vue 3
#      footgun); the fix is `toRefs()` or accessing through the proxy.
#   4. Emit one finding per match with `file` = the real .vue path and `line` =
#      the (1:1-mapped) .vue line, `sourceLine` = the verbatim .vue line so the
#      deterministic diff-bounded check and the LLM fix patch the real file.
#
# The detector ONLY extracts and PARSES — it never executes the SFC or any repo
# code (the wave-wide "never run repo code at scan time" rule; the temp file is a
# blanked copy, never imported or evaluated). The temp .ts is written under a
# mktemp dir and removed on exit.
#
# Contract (detect/command.go): argv = <interpreter> <script> <scope-root>.
# Output: JSON array of findings on stdout ("[]" = none). RequiredTools: python3
# (extraction + JSON emit) and ast-grep (the matcher) — the fixture gates on
# python3; ast-grep is the universally-probed matcher. Deterministic: file +
# line order.
set -eu

root="${1:-.}"

# The ast-grep rule (reactive() destructure) lives next to this script.
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
rule="$script_dir/reactivity.yml"

# All heavy lifting (extract → ast-grep → map → JSON) is in one python3 program so
# the line bookkeeping is exact and hermetic. python3 shells out to ast-grep per
# temp file; it never imports or runs the .vue/.ts content.
ROOT="$root" RULE="$rule" python3 - <<'PY'
import json, os, re, subprocess, sys, tempfile

root = os.environ["ROOT"]
rule = os.environ["RULE"]

script_open = re.compile(r'^\s*<script\b[^>]*\bsetup\b')
script_close = re.compile(r'^\s*</script>')

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

    # Build a line-preserving temp .ts: script lines verbatim, all others blank.
    in_script = False
    out = []
    for ln in lines:
        if not in_script and script_open.match(ln):
            in_script = True
            out.append("")  # blank the <script setup> open tag
            continue
        if in_script and script_close.match(ln):
            in_script = False
            out.append("")  # blank the </script> close tag
            continue
        out.append(ln if in_script else "")

    if all(s == "" for s in out):
        continue  # no <script setup> block

    with tempfile.NamedTemporaryFile("w", suffix=".ts", delete=False) as tmp:
        tmp.write("\n".join(out) + "\n")
        tmp_path = tmp.name

    try:
        proc = subprocess.run(
            ["ast-grep", "scan", "--rule", rule, "--json=compact", tmp_path],
            capture_output=True, text=True,
        )
        matches = json.loads(proc.stdout) if proc.stdout.strip() else []
    except (OSError, ValueError):
        matches = []
    finally:
        os.unlink(tmp_path)

    rel = os.path.relpath(vue, root)
    for m in matches:
        # ast-grep range.start.line is 0-based; the temp file is line-preserving,
        # so (0-based line + 1) is the 1-based line in the ORIGINAL .vue.
        line0 = m.get("range", {}).get("start", {}).get("line", 0)
        line = line0 + 1
        source = lines[line0] if 0 <= line0 < len(lines) else m.get("lines", "")
        findings.append({
            "file": rel,
            "line": line,
            "severity": "medium",
            "message": "Destructuring a reactive() object in <script setup> loses reactivity; use toRefs() or access via the proxy",
            "snippet": source,
            "sourceLine": source,
            "ruleId": "vue-reactivity-misuse",
        })

json.dump(findings, sys.stdout)
sys.stdout.write("\n")
PY
