# fastapi-hardcoded-secret fix prompt (LLM-assisted, SECURITY-stage — Sprint 024)

The detected line embeds a SECRET (an API key, token, password, or signing key)
directly in source as a string literal assigned to a credential-named target —
e.g. `SECRET_KEY = "..."`, `API_KEY = "..."`, `PASSWORD = "..."`. A hardcoded
secret is committed to history, shared with everyone who can read the repo, and
cannot be rotated without a code change — it is a credential leak.

Remediate it by REMOVING the literal and reading the value from the environment
at runtime, and by recording the variable so operators know to supply it:

1. Replace the hardcoded literal with an environment read. Use
   `os.environ["<VAR>"]` when the value is required (it raises if unset, failing
   fast), or `os.getenv("<VAR>", <default>)` when a default is acceptable. `<VAR>`
   is the credential-bearing target name (e.g. `SECRET_KEY` → `os.environ["SECRET_KEY"]`,
   `API_KEY` → `os.environ["API_KEY"]`). Keep the same assignment target so all
   downstream usage is unchanged once the variable is set.
2. Add `import os` if the file does not already import it.
3. Add the variable to `.env.example` as `<VAR>=` (NAME ONLY, no value — the
   example file documents which variables must be set; it must NEVER contain a
   real secret).
4. Change ONLY how the value reaches the code — keep the same target, type, and
   downstream usage so behavior is identical once the variable is set.

Constraints:
- Remove the secret literal ENTIRELY from the source — leaving any part of it
  behind keeps the leak (the detector-clears gate will reject a partial removal).
- Do NOT put the secret value (or any real credential) in `.env.example` — only
  the variable name and an empty value.
- Touch only the line(s) containing the finding plus the minimal `import os` /
  `.env.example` edits. Do not rewrite unrelated code.
- Return ONLY a unified diff. Do not include prose.

RATIONALE (surfaced to the reviewer): a secret committed in source is exposed to
everyone with repo access and is preserved forever in git history — it cannot be
revoked by deleting the line, only by ROTATING the credential. This fix stops the
ongoing leak by moving the value to an environment variable (supplied at runtime,
never committed) and recording the variable in `.env.example` so deployment still
works. It is staged for human review (auto_apply is false) because relocating a
credential is a security change and, critically, THE EXPOSED VALUE STILL MUST BE
ROTATED out of band — removing it from source does not un-leak what was already
committed. The verifier gate (detector-clears + a `python -m py_compile` parse
check) must pass: no secret-named target may still be assigned a string literal,
and the file must still parse.
