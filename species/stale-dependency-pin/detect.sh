#!/bin/sh
# stale-dependency-pin detector (Sprint 020 command escape hatch).
#
# Flags a dependency PIN duplicated within go.mod: the same module path required
# on more than one line. Each occurrence AFTER the first is reported as removable
# (the first pin is kept; the redundant ones are stale). This is cross-line
# analysis ast-grep cannot express. Cleanup-scoped — it removes a redundant pin,
# it does NOT change versions (that is Renovate's lane).
#
# Contract (detect/command.go): argv = <interpreter> <script> <scope-root>.
# Output: JSON array of findings on stdout ("[]" = none). Hermetic: pure POSIX
# sh + awk; no network, no `go`. Deterministic: go.mod line order.
set -eu

root="${1:-.}"
gomod="$root/go.mod"

if [ ! -f "$gomod" ]; then
	printf '[]\n'
	exit 0
fi

# A single awk pass does the whole job: track require-block state, extract each
# line's module path, count occurrences, and on a SECOND+ occurrence of a path
# emit a JSON finding for that line. The first occurrence of each path is kept.
# JSON string escaping (backslash, quote, tab) is done inline.
awk '
function esc(s,   r) {
	r = s
	gsub(/\\/, "\\\\", r)
	gsub(/"/, "\\\"", r)
	gsub(/\t/, "\\t", r)
	return r
}
BEGIN { inblock = 0; first = 1; printf "[" }
{
	line = $0
	# Enter/exit a grouped require ( ... ) block.
	if (line ~ /^require[[:space:]]*\(/) { inblock = 1; next }
	if (inblock && line ~ /^[[:space:]]*\)/) { inblock = 0; next }

	mod = ""
	if (line ~ /^require[[:space:]]+[^ (]/) {
		# single-line: require <path> <version>
		mod = line
		sub(/^require[[:space:]]+/, "", mod)
		sub(/[[:space:]].*$/, "", mod)
	} else if (inblock) {
		t = line
		sub(/^[[:space:]]+/, "", t)
		if (t == "" || t ~ /^\/\//) next   # blank or comment line in block
		mod = t
		sub(/[[:space:]].*$/, "", mod)
	}
	if (mod == "") next

	count[mod]++
	if (count[mod] > 1) {
		if (!first) printf ","
		first = 0
		printf "{\"file\":\"go.mod\",\"line\":%d,\"severity\":\"low\",\"message\":\"dependency pin \\\"%s\\\" is duplicated in go.mod; remove the redundant require\",\"snippet\":\"%s\",\"sourceLine\":\"%s\",\"ruleId\":\"stale-dependency-pin\"}", NR, mod, esc(line), esc(line)
	}
}
END { printf "]\n" }
' "$gomod"
