#!/bin/sh
# laravel-orphan-config-key detector (Sprint 023 PHP/Laravel wave, command escape
# hatch). Near-exact mirror of species/dead-config for Laravel PHP config.
#
# Flags a TOP-LEVEL key in a config/*.php array whose dotted path `<file>.<key>`
# is referenced NOWHERE via config('<file>.<key>') in the source tree — an orphan
# entry left after the consuming code was removed. Cross-file analysis ast-grep
# cannot do (the key lives in config/*.php; its uses, if any, are scattered across
# source). For each `'key' => ...` line in each config/*.php, the script builds the
# Laravel dotted path from the file basename and the key, then greps the rest of
# the tree (outside config/) for a config('file.key') / config("file.key") read;
# if it appears nowhere, the line is reported as removable.
#
# Contract (detect/command.go): argv = <interpreter> <script> <scope-root>.
# Output: JSON array of findings on stdout ("[]" = none). Hermetic: POSIX sh +
# grep/sed; no network; never runs the config (avoids executing repo code at scan
# time). Deterministic: config file + line order.
set -eu

root="${1:-.}"
cfgdir="$root/config"

if [ ! -d "$cfgdir" ]; then
	printf '[]\n'
	exit 0
fi

first=1
printf '['
# Iterate config/*.php deterministically (sorted).
for cfg in $(find "$cfgdir" -maxdepth 1 -name '*.php' -type f | sort); do
	base="$(basename "$cfg" .php)"
	rel="${cfg#"$root"/}"
	lineno=0
	while IFS= read -r line || [ -n "$line" ]; do
		lineno=$((lineno + 1))

		# Extract a top-level single/double-quoted string key from `  'name' => ...`.
		key="$(printf '%s' "$line" | sed -n "s/^[[:space:]]*['\"]\\([A-Za-z0-9_.-]*\\)['\"][[:space:]]*=>.*/\\1/p")"
		[ -z "$key" ] && continue

		dotted="$base.$key"

		# Is the dotted path referenced via config('file.key') / config("file.key")
		# anywhere OUTSIDE config/? Search source-ish PHP/Blade files.
		if grep -rqE "config\\(['\"]$dotted\\b" "$root" \
			--include='*.php' --include='*.blade.php' \
			--exclude-dir=config 2>/dev/null; then
			continue # referenced somewhere → not orphan
		fi

		esc="$(printf '%s' "$line" | sed 's/\\/\\\\/g; s/"/\\"/g' | sed 's/	/\\t/g')"
		[ "$first" -eq 0 ] && printf ','
		first=0
		printf '{"file":"%s","line":%s,"severity":"low","message":"config key %s%s%s is referenced nowhere via config(); it can be removed","snippet":"%s","sourceLine":"%s","ruleId":"laravel-orphan-config-key"}' \
			"$rel" "$lineno" '\"' "$dotted" '\"' "$esc" "$esc"
	done <"$cfg"
done
printf ']\n'
