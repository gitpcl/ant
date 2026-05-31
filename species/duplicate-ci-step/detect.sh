#!/bin/sh
# duplicate-ci-step detector (Sprint 020 command escape hatch).
#
# Flags a CI step whose `run:` command appears in more than one job: the same
# `- run: <command>` on more than one line. Each occurrence AFTER the first is
# reported as a possible consolidation candidate (the first is kept). Cross-block
# analysis ast-grep cannot express (the repetition spans separate jobs).
#
# REPORT-ONLY: this is advisory. Jobs run on isolated runners, so repeated setup/
# build steps across jobs are frequently REQUIRED, not redundant — Ant reports the
# smell and proposes NO change (the species declares fix.kind = "none"). Removing a
# cross-job step is unsafe (it can strip a deploy job's install/build); real
# consolidation is a human judgement call (reusable workflow / composite action /
# artifact passing).
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
			printf "{\"file\":\".github/workflows/ci.yml\",\"line\":%d,\"severity\":\"low\",\"message\":\"CI step \\\"run: %s\\\" appears in more than one job; if the jobs run on separate runners this may be REQUIRED, otherwise consider a reusable workflow/composite action (report-only — Ant proposes no change)\",\"snippet\":\"%s\",\"sourceLine\":\"%s\",\"ruleId\":\"duplicate-ci-step\"}", NR, esc(cmd), esc(line), esc(line)
		}
	}
}
END { printf "]\n" }
' "$wf"
