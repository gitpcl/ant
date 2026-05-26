#!/bin/sh
# dead-config detector (Sprint 020 command escape hatch).
#
# Flags a top-level config KEY in config.json whose name is referenced NOWHERE in
# the source tree — an orphan entry left after the consuming code was removed.
# Cross-file analysis ast-grep cannot do (the key is in the config; its uses, if
# any, are scattered across source files). For each `"key": ...` line in
# config.json, the script greps the rest of the tree for the bare key name; if it
# appears nowhere outside config.json, the line is reported as removable.
#
# Contract (detect/command.go): argv = <interpreter> <script> <scope-root>.
# Output: JSON array of findings on stdout ("[]" = none). Hermetic: POSIX sh +
# grep/sed; no network. Deterministic: config.json line order.
set -eu

root="${1:-.}"
cfg="$root/config.json"

if [ ! -f "$cfg" ]; then
	printf '[]\n'
	exit 0
fi

lineno=0
first=1
printf '['
while IFS= read -r line || [ -n "$line" ]; do
	lineno=$((lineno + 1))

	# Extract a top-level JSON key from `  "name": value` lines. Skip lines that
	# are not a simple key declaration (braces, array items, blanks).
	key="$(printf '%s' "$line" | sed -n 's/^[[:space:]]*"\([A-Za-z0-9_]*\)"[[:space:]]*:.*/\1/p')"
	[ -z "$key" ] && continue

	# Is the key referenced anywhere OUTSIDE config.json? Search source-ish files.
	if grep -rqE "\\b$key\\b" "$root" \
		--include='*.go' --include='*.ts' --include='*.js' --include='*.py' 2>/dev/null; then
		continue # referenced somewhere → not dead
	fi

	esc="$(printf '%s' "$line" | sed 's/\\/\\\\/g; s/"/\\"/g' | sed 's/	/\\t/g')"
	[ "$first" -eq 0 ] && printf ','
	first=0
	printf '{"file":"config.json","line":%s,"severity":"low","message":"config key %s%s%s is referenced nowhere in the codebase; it can be removed","snippet":"%s","sourceLine":"%s","ruleId":"dead-config"}' \
		"$lineno" '\"' "$key" '\"' "$esc" "$esc"
done <"$cfg"
printf ']\n'
