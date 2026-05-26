#!/bin/sh
# hardcoded-secret SECRET-SCANNER-CLEARS verifier (Sprint 021 P6 — command:
# escape hatch from Sprint 020).
#
# This is the species' remediation proof. It runs in the SCRATCH COPY of the tree
# (verify/command.go applies the proposed diff first), so it observes the POST-FIX
# state. It re-runs the SAME secret-detection rules detect.sh uses, but in
# PASS/FAIL mode: if ANY secret literal still remains anywhere in the tree, it
# prints the offending lines and exits NON-ZERO, failing the gate — so a fix that
# left the secret behind (or only half-removed it) is rejected and never staged.
# If the scanner finds nothing, it exits 0: the secret is gone, the remediation is
# proven (not merely detected).
#
# This is what makes hardcoded-secret more than "another SAST": the gate genuinely
# re-runs the scanner after the fix and demands a clean tree. It is intentionally
# INDEPENDENT of detector-clears (which re-runs the species' own detector): two
# separate authorities must agree the secret is gone.
#
# Hermetic & offline: pure POSIX sh + find/awk; no network, no installed scanner.
# CI does NOT depend on a real third-party scanner being present — the scanner is
# this self-contained stub, exactly the fake-tool pattern Sprint 017/020 use.
set -eu

root="."

# Collect candidate Go files into the positional params so awk OPENS each (so
# FILENAME/FNR work), mirroring detect.sh.
set --
for f in $(find "$root" -type f -name '*.go' 2>/dev/null | LC_ALL=C sort); do
	set -- "$@" "$f"
done
if [ "$#" -eq 0 ]; then
	echo "secret-scanner: no Go source to scan; clean"
	exit 0
fi

# Re-use detect.sh's rules in pass/fail mode. We print every offending line and
# count them; a non-zero count fails the gate. The awk program mirrors detect.sh
# RULE-FOR-RULE so the two authorities cannot drift.
hits="$(awk '
	function entropy(s,   i, c, n, freq, p, h) {
		n = length(s); if (n == 0) return 0
		for (i = 1; i <= n; i++) { c = substr(s, i, 1); freq[c]++ }
		h = 0
		for (c in freq) { p = freq[c] / n; h -= p * (log(p) / log(2)) }
		for (c in freq) delete freq[c]
		return h
	}
	function literal(line,   m) {
		g_val = ""
		if (match(line, /"[^"]*"/)) { g_val = substr(line, RSTART + 1, RLENGTH - 2); return 1 }
		return 0
	}
	FNR == 1 { file = FILENAME }
	{
		line = $0
		if (line ~ /os\.Getenv/) next
		if (!literal(line)) next
		val = g_val
		if (val ~ /^AKIA[A-Z0-9]{16}$/) { print file ":" FNR ": " line; next }
		if (val ~ /^ghp_[A-Za-z0-9]{36}$/) { print file ":" FNR ": " line; next }
		if (val ~ /^xoxb-[A-Za-z0-9-]{10,}$/) { print file ":" FNR ": " line; next }
		if (val ~ /BEGIN[ ]+(RSA[ ]+)?PRIVATE KEY/) { print file ":" FNR ": " line; next }
		if (line !~ /(^|[^A-Za-z0-9_])((const|var)[ \t]+)?[A-Za-z0-9_]*([Kk]ey|[Tt]oken|[Ss]ecret|[Pp]assword|[Pp]asswd|[Aa]pi[Kk]ey|APIKey|APIKEY)[A-Za-z0-9_]*[ \t]*:?=[ \t]*"/) next
		if (length(val) < 20) next
		if (entropy(val) < 3.5) next
		print file ":" FNR ": " line
	}
' "$@")"

if [ -n "$hits" ]; then
	echo "secret-scanner: secret(s) STILL PRESENT after the fix — remediation incomplete:"
	printf '%s\n' "$hits"
	exit 1
fi

echo "secret-scanner: clean — no hardcoded secret remains after the fix"
exit 0
