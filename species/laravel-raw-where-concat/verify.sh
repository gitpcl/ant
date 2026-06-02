#!/bin/sh
# laravel-raw-where-concat verifier (Sprint 023 PHP/Laravel wave, command: escape
# hatch — the shared php -l parse gate for this wave).
#
# Runs in the SCRATCH COPY (verify/command.go applies the proposed diff first), so
# this proves every PHP file STILL PARSES after the raw fragment was rewritten to
# bound parameters — the parse gate the sprint-023 contract requires in place of
# the (vacuous on a non-Go repo) Go-build compile check. A rewrite that left a
# syntax error makes `php -l` exit non-zero, the check fails, and the fix is
# skipped (never staged).
#
# `php -l` is run over every .php file under the tree (find + a loop) so the gate
# is file-agnostic — it does not need to know which file the fix touched. stderr
# is discarded because some PHP builds emit benign extension-load warnings on a
# successful lint; only the EXIT CODE gates (php -l exits non-zero only on a real
# parse error). RequiredTools=["php"] in the fixture skips this case when php is
# absent, exactly as the ast-grep species skip without the matcher — so CI without
# php stays green while the gate runs for real where present.
#
# This verifier NEVER EXECUTES the repo's PHP (no `require`/`include`/`-r require`)
# — `php -l` only parses. Authoring a SECURITY species that ran untrusted,
# post-diff PHP at verify time would itself be a code-execution surface; the parse
# gate is deliberately execution-free.
set -eu

found=0
for f in $(find . -name '*.php' -type f); do
	found=1
	php -l "$f" >/dev/null 2>&1 || {
		echo "php -l failed: $f"
		php -l "$f" 2>&1 || true
		exit 1
	}
done

if [ "$found" -eq 0 ]; then
	echo "no PHP files to lint"
fi
exit 0
