# hardcoded-secret fix prompt (LLM-assisted, ADR-0002) — SECURITY (Sprint 021 P6)

The detected line embeds a SECRET (an API key, token, password, or private key)
directly in source as a string literal. A hardcoded secret is committed to
history, shared with everyone who can read the repo, and cannot be rotated
without a code change — it is a credential leak.

Remediate it by REMOVING the literal and reading the value from the environment
at runtime, and by recording the variable so operators know to supply it:

1. Replace the hardcoded literal with a read from an environment variable:
   `os.Getenv("<VAR>")` (Go), where `<VAR>` is an UPPER_SNAKE_CASE name derived
   from the credential-bearing identifier (e.g. `apiKey` → `API_KEY`,
   `dbPassword` → `DB_PASSWORD`, an AWS key → `AWS_ACCESS_KEY_ID`).
2. Add `import "os"` if the file does not already import it.
3. Add the variable to `.env.example` as `<VAR>=` (NAME ONLY, no value — the
   example file documents which variables must be set; it must never contain a
   real secret).
4. Change ONLY how the value reaches the code — keep the same identifier, type,
   and downstream usage so behavior is identical once the variable is set.

Constraints:
- Remove the secret literal ENTIRELY from the source — leaving any part of it
  behind keeps the leak (the secret-scanner gate will reject a partial removal).
- Do NOT put the secret value (or any real credential) in `.env.example` — only
  the variable name and an empty value.
- Touch only the line(s) containing the finding plus the minimal import / config-
  example edits. Do not rewrite unrelated code.
- Return ONLY a unified diff. Do not include prose.

RATIONALE (surfaced to the reviewer): a secret committed in source is exposed to
everyone with repo access and is preserved forever in git history — it cannot be
revoked by deleting the line, only by ROTATING the credential. This fix stops the
ongoing leak by moving the value to an environment variable (supplied at runtime,
never committed) and recording the variable in `.env.example` so deployment still
works. It is staged for human review (auto_apply is false) because relocating a
credential is a security change and, critically, THE EXPOSED VALUE STILL MUST BE
ROTATED out of band — removing it from source does not un-leak what was already
committed. The verifier gate (compile + a secret-scanner-clears re-scan +
detector-clears) must pass: the rewrite must build, and BOTH the scanner and the
species' own detector must find no secret remaining.
