#!/bin/sh
# fastapi-hardcoded-secret verifier (Sprint 024 Python/FastAPI wave, command:
# escape hatch — the shared `python -m py_compile` parse gate for this wave).
#
# Runs in the SCRATCH COPY (verify/command.go applies the proposed diff first), so
# this proves every Python file STILL PARSES after the secret literal was rewritten
# to an environment read — the parse gate the sprint-024 contract requires in place
# of the (vacuous on a non-Go repo) Go-build compile check. A rewrite that left a
# syntax error (e.g. a dangling `os.environ[` or a missing `import os`) makes
# `py_compile` exit non-zero, the check fails, and the fix is skipped (never
# staged).
#
# `python -m py_compile` is run over every .py file under the tree (find + a loop)
# so the gate is file-agnostic — .env.example is not a .py file and is not
# compiled. stderr is discarded; only the EXIT CODE gates. RequiredTools=["python3"]
# in the fixture skips this case when python3 is absent, exactly as the ast-grep
# species skip without the matcher — so CI without python3 stays green while the
# gate runs for real where present.
#
# This verifier NEVER EXECUTES the repo's Python: `python -m py_compile` only
# byte-compiles the source to a .pyc (it parses and compiles, it does NOT import
# or run module top-level code — so the post-fix `os.environ["..."]` read is never
# evaluated, and an unset variable cannot crash the gate). Authoring a SECURITY
# species that ran untrusted, post-diff Python at verify time would itself be a
# code-execution surface; the parse gate is deliberately execution-free.
set -eu

found=0
for f in $(find . -name '*.py' -type f); do
	found=1
	python3 -m py_compile "$f" >/dev/null 2>&1 || {
		echo "py_compile failed: $f"
		python3 -m py_compile "$f" 2>&1 || true
		exit 1
	}
done

if [ "$found" -eq 0 ]; then
	echo "no Python files to compile"
fi
exit 0
