#!/bin/sh
# unused-dependency detector (Sprint 020 command escape hatch).
#
# Cross-references the dependencies DECLARED in go.mod against the import paths
# actually USED across the module's .go files, and emits a JSON finding for every
# `require`d module that is never imported. This is whole-tree analysis ast-grep
# cannot do — ast-grep matches within a single file's AST; "declared here, used
# nowhere" spans the manifest plus every source file.
#
# Contract (detect/command.go): argv = <interpreter> <this script> <scope-root>.
# Output: a JSON array of findings on stdout (empty / "[]" = no findings). Exit 0
# on success; the engine treats a non-JSON payload as a loud failure.
#
# Hermetic & offline: pure POSIX sh + grep/sed; no network, no `go` invocation.
# Deterministic: requires are emitted in go.mod line order.

set -eu

root="${1:-.}"
gomod="$root/go.mod"

# No manifest → nothing to analyze. Emit an empty array (valid "no findings").
if [ ! -f "$gomod" ]; then
	printf '[]\n'
	exit 0
fi

# Collect the set of imported module paths across every .go file. We extract the
# quoted import path from both single imports (`import "x"`) and grouped import
# blocks (`\t"x"`). The module path of a dependency is a PREFIX of the import
# path (e.g. require rsc.io/quote → import "rsc.io/quote/v3"), so the usage test
# below is a prefix match, not equality.
imports="$(grep -rhoE '"[a-zA-Z0-9_./-]+"' "$root" --include='*.go' 2>/dev/null \
	| sed 's/"//g' \
	| sort -u || true)"

# is_used <module-path> → 0 (used) if any import equals it or is under it.
is_used() {
	mod="$1"
	# Exact match or import path is "<mod>/...". Iterate the import set.
	printf '%s\n' "$imports" | while IFS= read -r imp; do
		[ -z "$imp" ] && continue
		case "$imp" in
		"$mod" | "$mod"/*) exit 0 ;;
		esac
	done
	# The subshell above exits 0 only via the `exit 0` inside the loop; translate
	# its status into ours.
	if printf '%s\n' "$imports" | grep -qxF "$mod"; then
		return 0
	fi
	if printf '%s\n' "$imports" | grep -qE "^$(printf '%s' "$mod" | sed 's/[.[\*^$/]/\\&/g')/"; then
		return 0
	fi
	return 1
}

# Walk go.mod line by line, tracking whether we're inside a `require ( ... )`
# block, and emit a finding for each required module path not used anywhere.
# We capture the VERBATIM line (with its leading tab inside a block) as sourceLine
# so the deterministic delete-match fix patches a line that byte-matches the tree.
emit_findings() {
	in_block=0
	lineno=0
	first=1
	printf '['
	while IFS= read -r line || [ -n "$line" ]; do
		lineno=$((lineno + 1))

		# Enter/exit a grouped require block.
		case "$line" in
		'require ('*) in_block=1; continue ;;
		')'*) [ "$in_block" -eq 1 ] && in_block=0; continue ;;
		esac

		mod=""
		case "$line" in
		'require '*)
			# single-line require: `require <path> <version>`
			mod="$(printf '%s' "$line" | sed -E 's/^require[[:space:]]+//; s/[[:space:]].*$//')"
			;;
		*)
			if [ "$in_block" -eq 1 ]; then
				# block line: `\t<path> <version>` (skip `// indirect`-only blanks)
				trimmed="$(printf '%s' "$line" | sed -E 's/^[[:space:]]+//')"
				[ -z "$trimmed" ] && continue
				mod="$(printf '%s' "$trimmed" | sed -E 's/[[:space:]].*$//')"
			fi
			;;
		esac

		[ -z "$mod" ] && continue
		# `// indirect` deps are pulled transitively; only flag DIRECT requires.
		case "$line" in *'// indirect'*) continue ;; esac

		if ! is_used "$mod"; then
			# Escape the verbatim source line for JSON (backslash, quote, tab).
			esc="$(printf '%s' "$line" | sed 's/\\/\\\\/g; s/"/\\"/g' | sed 's/	/\\t/g')"
			[ "$first" -eq 0 ] && printf ','
			first=0
			printf '{"file":"go.mod","line":%s,"severity":"medium","message":"dependency %s%s%s is declared in go.mod but never imported","snippet":"%s","sourceLine":"%s","ruleId":"unused-dependency"}' \
				"$lineno" '\"' "$mod" '\"' "$esc" "$esc"
		fi
	done <"$gomod"
	printf ']\n'
}

emit_findings
