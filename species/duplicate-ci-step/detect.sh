#!/bin/sh
# duplicate-ci-step detector (Sprint 020 command escape hatch).
#
# Flags a CI step whose `run:` command is duplicated across the workflow YAML:
# the same `- run: <command>` appearing on more than one line. Each occurrence
# AFTER the first is reported as a consolidation candidate (the first is kept).
# Cross-block analysis ast-grep cannot express (the duplication spans separate
# jobs). Cleanup-scoped: it points out the duplicate so it can be consolidated.
#
# Contract (detect/command.go): argv = <interpreter> <script> <scope-root>.
# Output: JSON array of findings on stdout ("[]" = none). Hermetic: POSIX sh +
# awk; no network. Deterministic: workflow line order. The workflow file is
# discovered at the conventional path .github/workflows/ci.yml under the scope.
set -eu

root="${1:-.}"
wf="$root/.github/workflows/ci.yml"

if [ ! -f "$wf" ]; then
	printf '[]\n'
	exit 0
fi

# A single awk pass: normalize each `- run: <cmd>` step to its command, count
# occurrences, and on a SECOND+ occurrence emit a finding for that line. JSON
# escaping (backslash, quote, tab) is inline. Only `- run:` steps are considered
# (the common duplicated-command case); other step keys are ignored.
awk '
function esc(s,   r) {
	r = s
	gsub(/\\/, "\\\\", r)
	gsub(/"/, "\\\"", r)
	gsub(/\t/, "\\t", r)
	return r
}
BEGIN { first = 1; printf "[" }
{
	line = $0
	# Match a run step: optional indent, "- run:", then the command.
	if (line ~ /^[[:space:]]*-[[:space:]]+run:[[:space:]]*/) {
		cmd = line
		sub(/^[[:space:]]*-[[:space:]]+run:[[:space:]]*/, "", cmd)
		count[cmd]++
		if (count[cmd] > 1) {
			if (!first) printf ","
			first = 0
			printf "{\"file\":\".github/workflows/ci.yml\",\"line\":%d,\"severity\":\"low\",\"message\":\"CI step \\\"run: %s\\\" is duplicated across jobs; consolidate it into a reusable job/composite action/script\",\"snippet\":\"%s\",\"sourceLine\":\"%s\",\"ruleId\":\"duplicate-ci-step\"}", NR, esc(cmd), esc(line), esc(line)
		}
	}
}
END { printf "]\n" }
' "$wf"
