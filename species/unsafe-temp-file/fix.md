# unsafe-temp-file fix prompt (LLM-assisted, ADR-0002) — SECURITY (Sprint 021 P6)

The detected literal is a HARDCODED, predictable temp path (a string beginning
`/tmp/`). Writing to a fixed path in a world-writable directory is a local-attack
surface: an attacker can pre-create the path as a SYMLINK (redirecting the write
to a file your process can reach — a symlink/TOCTOU attack), or race to read or
clobber the predictable file.

Fix it by creating the temp file through a SECURE temp API:

1. Replace the hardcoded path + direct write with `os.CreateTemp(dir, pattern)`
   (use `""` for the dir to get the system temp dir; a pattern like
   `"app-cache-*.tmp"` so the name stays recognizable). It returns an OPEN
   `*os.File` and an error: the OS chooses an unpredictable name and creates the
   file atomically with 0600 permissions.
2. Handle the returned error (a failed create must not be ignored), `defer
   f.Close()`, and write the data through the handle (`f.Write(data)`).
3. Return `f.Name()` where the old code returned the hardcoded path, so callers
   still receive the actual file path.
4. If the code created a temp DIRECTORY, use `os.MkdirTemp` analogously.
5. Keep the function signature and the caller contract unchanged — change ONLY how
   the temp file is created and named.

Constraints:
- Remove the hardcoded `/tmp/...` literal entirely — no predictable path may
  remain (otherwise the attack surface is still present).
- Handle every error the secure temp API returns; do not discard it.
- Touch only the function containing the finding and its imports.
- Return ONLY a unified diff. Do not include prose.

RATIONALE (surfaced to the reviewer): a fixed temp path is guessable, so an
attacker who shares the temp directory can plant a symlink or pre-create the file
to hijack or corrupt the write before the program runs — a well-known local
privilege/integrity attack. os.CreateTemp closes the window: the OS picks a name
the attacker cannot predict and creates the file with restrictive permissions in
one atomic step. This fix is staged for human review (auto_apply is false) because
the correct temp API, permissions, and cleanup are judgement calls. The verifier
gate (compile + tests:affected + detector-clears) must pass: the rewrite must build
with the new handle/error flow, keep the affected test green, and leave no
hardcoded /tmp path.
