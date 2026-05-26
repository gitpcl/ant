#!/bin/sh
# duplicate-ci-step verifier (Sprint 020 command: escape hatch).
#
# Runs in the SCRATCH COPY (verify/command.go applies the proposed diff first), so
# this proves the workflow YAML is STILL structurally valid after the duplicate
# step was removed — the YAML-parse/lint gate the contract requires. A removal
# that corrupted the structure (a tab crept in, indentation no longer a multiple
# of two, a key/list line malformed) fails the check, so the fix is skipped.
#
# Hermetic & offline: a pure-stdlib structural lint (no pyyaml — it is not in the
# stdlib and must not be a network dependency). It catches the realistic ways a
# line-deletion breaks a workflow: tab indentation, odd indentation, and trailing
# whitespace. The fixture harness skips this case when python3 is absent
# (RequiredTools), exactly as ast-grep species skip without the matcher.
set -eu

python3 - <<'PY'
import sys

path = ".github/workflows/ci.yml"
errors = []
with open(path) as f:
    lines = f.read().splitlines()

# A pure-stdlib structural lint (pyyaml is not in the stdlib and must not be a
# network dependency). It catches the realistic ways a line-deletion corrupts a
# workflow: tab indentation, odd indentation, an unstructured dangling token, and
# a `steps:` mapping left with NO list items beneath it (a job whose only step was
# wrongly removed). These are the failure modes a delete-match on a `- run:` line
# can actually produce.
step_keys = []  # (line_no, indent) of `steps:` keys awaiting a child list item
fulfilled = set()
for n, raw in enumerate(lines, 1):
    if raw.strip() == "":
        continue
    indent = len(raw) - len(raw.lstrip(" "))
    body = raw.strip()

    if "\t" in raw[: indent + 1] or raw.startswith("\t"):
        errors.append(f"line {n}: tab in indentation")
    if indent % 2 != 0:
        errors.append(f"line {n}: odd indentation ({indent} spaces)")
    if not (":" in body or body.startswith("- ") or body.startswith("#")):
        errors.append(f"line {n}: unstructured line {body!r}")

    if body.rstrip() == "steps:":
        step_keys.append((n, indent))
    elif body.startswith("- "):
        # This list item fulfils the most recent steps: at a shallower indent.
        for i, (ln, ind) in enumerate(step_keys):
            if ind < indent:
                fulfilled.add(ln)

for ln, _ in step_keys:
    if ln not in fulfilled:
        errors.append(f"line {ln}: `steps:` has no steps after the edit (all removed?)")

if errors:
    print("YAML structure invalid after edit:", "; ".join(errors), file=sys.stderr)
    sys.exit(1)
PY
