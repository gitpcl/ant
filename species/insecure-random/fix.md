# insecure-random fix prompt (LLM-assisted, ADR-0002) — SECURITY (Sprint 021 P6)

The detected call generates a value with `math/rand`, a PREDICTABLE pseudo-random
generator. When that value is security-sensitive — a session token, password-reset
id, API key, salt, or nonce — predictability is a vulnerability: an attacker who
can observe or guess the seed (it is often the time, or a fixed default) can
reproduce the value and impersonate a user or forge a credential.

Fix it by generating the value with the cryptographically-secure RNG instead:

1. Replace the `math/rand` import with `crypto/rand` (and `encoding/hex` or
   `encoding/base64` if you encode the bytes to a string).
2. Generate the value by reading crypto-strong bytes: allocate a byte slice of the
   required length and fill it with `rand.Read(b)` (crypto/rand's Read), which
   returns an error you MUST handle — a failed read must not silently yield a weak
   or empty value.
3. Encode the bytes to the same output shape the caller expects (e.g.
   `hex.EncodeToString(b)` for a hex token of the same length).
4. Keep the function signature, the value's length/format, and the call sites
   unchanged — change ONLY the source of randomness.

Constraints:
- Remove the `math/rand` usage entirely — no `rand.Intn`/`Int63`/… call may
  remain (otherwise the predictable source is still in play).
- Handle the error crypto/rand's Read returns; do not discard it.
- Touch only the function containing the finding and its imports.
- Return ONLY a unified diff. Do not include prose.

RATIONALE (surfaced to the reviewer): math/rand is deterministic — given the seed,
every output is reproducible — so a token or key built with it can be predicted or
brute-forced, defeating the security guarantee the value is supposed to provide.
crypto/rand reads the operating system's CSPRNG, which is unpredictable even to an
attacker who knows the algorithm. This fix is staged for human review (auto_apply
is false) because choosing the right crypto API and confirming the value is
genuinely security-sensitive are judgement calls. The verifier gate (compile +
tests:affected + detector-clears) must pass: the rewrite must build with the new
error path, keep the affected tests green, and leave no math/rand generator call.
