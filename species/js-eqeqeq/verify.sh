#!/bin/sh
# js-eqeqeq verifier (Sprint 025 JS/TS + Vue wave, command: escape hatch —
# the shared `tsc --noEmit` type-check gate for the plain JS/TS species).
#
# Runs in the SCRATCH COPY (verify/command.go applies the proposed diff first), so
# this proves the project STILL TYPE-CHECKS after the debug statement was removed —
# the type-check gate the sprint-025 contract requires in place of the (vacuous on
# a JS/TS repo) Go-build compile check. A delete that left a syntax/type error
# makes `tsc --noEmit` exit non-zero, the check fails, and the fix is skipped
# (never staged).
#
# `tsc --noEmit` only type-checks; it NEVER executes the repo code, so the verifier
# is execution-free (the wave-wide "never run repo code at scan/verify time" rule).
# It is invoked with explicit flags rather than reading a tsconfig so the gate is
# self-contained and file-agnostic over the fixture's loose .ts/.js files:
#   --allowJs lets the same gate cover the .js cases; --skipLibCheck avoids pulling
#   in @types; --strict is off so an un-annotated fixture still type-checks.
#
# RequiredTools=["node","tsc"] in the fixture skips this case when the toolchain is
# absent, exactly as the ast-grep species skip without the matcher — so CI without
# node/tsc stays green while the gate runs for real where present.
set -eu

# Collect the .ts/.tsx/.js/.jsx files in the scratch tree (node_modules excluded).
set --
for f in $(find . -type f \( -name '*.ts' -o -name '*.tsx' -o -name '*.js' -o -name '*.jsx' \) \
	-not -path './node_modules/*' | sort); do
	set -- "$@" "$f"
done

if [ "$#" -eq 0 ]; then
	echo "no TS/JS files to type-check"
	exit 0
fi

tsc --noEmit --allowJs --skipLibCheck --moduleResolution node --target es2020 "$@"
