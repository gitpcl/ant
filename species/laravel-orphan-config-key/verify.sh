#!/bin/sh
# laravel-orphan-config-key verifier (Sprint 023 PHP/Laravel wave, command: escape
# hatch). PARSE-ONLY config gate — the trust-model-aligned mirror of
# species/dead-config's NON-executing config parse (python3 json.load).
#
# Runs in the SCRATCH COPY (verify/command.go applies the proposed diff first), so
# this proves every config/*.php is STILL a syntactically valid PHP file that
# returns an array literal after the orphan key was removed — the config-parse gate
# the sprint-023 contract requires (in place of the vacuous-on-a-non-Go-repo
# Go-build compile check). A removal that left a dangling `=>`, unbalanced bracket,
# or trailing-comma damage fails `php -l` (syntax) or the array-shape token check,
# the check exits non-zero, and the fix is skipped (never staged).
#
# TRUST MODEL (security review, Sprint 023): this gate is DELIBERATELY parse-only.
# It does NOT `require`/`include` the config — executing it would run arbitrary repo
# PHP (and, worse, run it on the SCRATCH tree AFTER an LLM-/attacker-influenceable
# diff was applied), a code-execution surface at verify time. Instead it (1) runs
# `php -l` for syntax, then (2) TOKENIZES the file with `token_get_all` (a lexer,
# not an evaluator) and asserts the first significant statement is `return` opening
# an array (`[` or `array(`) — the shape Laravel's config loader needs — WITHOUT
# evaluating a single line. This matches the Go sibling dead-config, which parses
# config.json without executing it, and the wave-wide "never run repo code" rule.
#
# stderr from php is discarded (some PHP builds emit benign extension-load warnings
# on success); only the EXIT CODE gates. RequiredTools=["php"] skips this case when
# php is absent, exactly as the ast-grep species skip without the matcher — CI
# without php stays green while the gate runs for real where present.
set -eu

# Non-executing array-shape check: tokenize the file (token_get_all is a lexer, it
# never evaluates code) and require the first significant token to be T_RETURN and
# the next to open an array literal. Exits 1 on any other shape or on a tokenizer
# failure. The script text is passed to `php -r` (single PHP arg) — no repo code is
# included or run.
shape_check='
$src = @file_get_contents($argv[1]);
if ($src === false) { exit(1); }
$toks = @token_get_all($src);
if (!is_array($toks)) { exit(1); }
$sig = [];
foreach ($toks as $t) {
    if (is_array($t)) {
        if (in_array($t[0], [T_WHITESPACE, T_COMMENT, T_DOC_COMMENT, T_OPEN_TAG], true)) { continue; }
        $sig[] = $t[0];
    } else {
        $sig[] = $t;
    }
    if (count($sig) >= 2) { break; }
}
if (($sig[0] ?? null) !== T_RETURN) { exit(1); }
$second = $sig[1] ?? null;
if ($second === "[" || $second === T_ARRAY) { exit(0); }
exit(1);
'

for cfg in $(find ./config -maxdepth 1 -name '*.php' -type f 2>/dev/null | sort); do
	php -l "$cfg" >/dev/null 2>&1 || {
		echo "php -l failed: $cfg"
		php -l "$cfg" 2>&1 || true
		exit 1
	}
	php -r "$shape_check" "$cfg" >/dev/null 2>&1 || {
		echo "config no longer returns an array literal (parse-only check): $cfg"
		exit 1
	}
done
exit 0
