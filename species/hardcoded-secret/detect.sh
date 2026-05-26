#!/bin/sh
# hardcoded-secret detector (Sprint 021 P6 — Security Hygiene; command escape
# hatch from Sprint 020).
#
# Flags a likely hardcoded secret/token/key embedded as a string literal in a Go
# source file. Two value-level signals (neither expressible in ast-grep's
# structural matcher, which cannot inspect a literal's bytes or compute entropy):
#
#   1. KNOWN TOKEN SHAPE — the literal matches a well-known credential format:
#      AWS access key id (AKIA + 16 base32), GitHub PAT (ghp_ + 36), Slack bot
#      token (xoxb-...), or a PEM private-key header. These are unambiguous.
#   2. HIGH-ENTROPY SECRET ASSIGNMENT — a string literal of length >= 20 assigned
#      to (or declared as) an identifier whose name contains key/token/secret/
#      password/passwd/apikey, AND whose Shannon entropy exceeds ~3.5 bits/char
#      (random credentials are high-entropy; ordinary prose/identifiers are not).
#
# Contract (detect/command.go): argv = <interpreter> <script> <scope-root>.
# Output: a JSON array of findings on stdout ("[]" = none). Exit 0 on success; a
# non-JSON payload is a loud failure. Hermetic: POSIX sh + grep/sed/awk; no
# network. Deterministic: files in sorted order, findings in line order.
#
# The detector NOMINATES (ADR-0004 candidate tier): it cannot prove a literal is a
# live credential. The verify gate confirms the specific finding — the llm fix
# moves the value to os.Getenv + .env.example, then `compile` + the SECRET-
# SCANNER-CLEARS command gate + detector-clears prove the rewrite builds and that
# no secret literal remains.
set -eu

root="${1:-.}"

# Print findings. We reuse one awk program (also used by scan.sh in pass/fail
# mode) so the detector and the scanner-clears gate agree byte-for-byte on what
# counts as a secret.
emit() {
	# Collect candidate Go source files deterministically into the positional
	# params, so awk OPENS each file (FILENAME/FNR work) rather than reading a
	# list off stdin. .env.example is NEVER scanned (it documents variable NAMES,
	# not values). Fixture paths contain no spaces; newline-split is sufficient and
	# portable.
	set --
	for f in $(find "$root" -type f -name '*.go' 2>/dev/null | LC_ALL=C sort); do
		set -- "$@" "$f"
	done
	[ "$#" -eq 0 ] && { printf '[]\n'; return; }

	awk '
		# --- Shannon entropy (bits/char) of the passed string ---
		function entropy(s,   i, c, n, freq, p, h) {
			n = length(s)
			if (n == 0) return 0
			for (i = 1; i <= n; i++) { c = substr(s, i, 1); freq[c]++ }
			h = 0
			for (c in freq) { p = freq[c] / n; h -= p * (log(p) / log(2)) }
			for (c in freq) delete freq[c]
			return h
		}
		# extract the FIRST double-quoted string literal on the line into g_val
		function literal(line,   m) {
			g_val = ""
			if (match(line, /"[^"]*"/)) {
				g_val = substr(line, RSTART + 1, RLENGTH - 2)
				return 1
			}
			return 0
		}
		function jsonesc(s) {
			gsub(/\\/, "\\\\", s); gsub(/"/, "\\\"", s); gsub(/\t/, "\\t", s)
			return s
		}
		function flag(file, lno, line, why,   esc) {
			esc = jsonesc(line)
			if (!started) { printf "[" ; started = 1 } else printf ","
			printf "{\"file\":\"%s\",\"line\":%d,\"severity\":\"high\",\"message\":\"%s\",\"snippet\":\"%s\",\"sourceLine\":\"%s\",\"ruleId\":\"hardcoded-secret\"}", \
				file, lno, why, esc, esc
		}
		BEGIN { started = 0 }
		FNR == 1 { file = FILENAME }
		{
			line = $0
			# Skip lines that read from the environment already (the fixed shape)
			# and import lines.
			if (line ~ /os\.Getenv/) next

			if (!literal(line)) next
			val = g_val

			# Rule 1: known token shapes (match the VALUE).
			if (val ~ /^AKIA[A-Z0-9]{16}$/) { flag(file, FNR, line, "hardcoded AWS access key id (AKIA...) — move it to an environment variable and rotate the key"); next }
			if (val ~ /^ghp_[A-Za-z0-9]{36}$/) { flag(file, FNR, line, "hardcoded GitHub personal access token (ghp_...) — move it to an environment variable and revoke the token"); next }
			if (val ~ /^xoxb-[A-Za-z0-9-]{10,}$/) { flag(file, FNR, line, "hardcoded Slack bot token (xoxb-...) — move it to an environment variable and rotate the token"); next }
			if (val ~ /BEGIN[ ]+(RSA[ ]+)?PRIVATE KEY/) { flag(file, FNR, line, "hardcoded private key material — move it to an environment variable / secret store and rotate"); next }

			# Rule 2: high-entropy literal ASSIGNED to a secret-named identifier.
			# Require an actual Go assignment/declaration whose TARGET is a
			# credential-named identifier and whose value is THIS string literal:
			#   [const|var] <name(key|token|secret|password|...)> [:]= "literal"
			# Anchoring on the assignment shape (the identifier immediately before
			# `=`/`:=`, then the quote) excludes function-call arguments like
			# `t.Fatalf("...%s...", v)` whose text merely mentions a key name — those
			# are not secret assignments and must not be flagged.
			if (line !~ /(^|[^A-Za-z0-9_])((const|var)[ \t]+)?[A-Za-z0-9_]*([Kk]ey|[Tt]oken|[Ss]ecret|[Pp]assword|[Pp]asswd|[Aa]pi[Kk]ey|APIKey|APIKEY)[A-Za-z0-9_]*[ \t]*:?=[ \t]*"/) next
			if (length(val) < 20) next
			if (entropy(val) < 3.5) next
			flag(file, FNR, line, "likely hardcoded secret assigned to a credential-named variable (high-entropy literal) — move it to an environment variable and rotate the value")
		}
		END { if (!started) printf "[" ; printf "]\n" }
	' "$@"
}

emit
