# magic-number fix prompt (LLM-assisted, ADR-0002) — Sprint 019

The detected statement uses an unexplained numeric literal (a "magic number").
Its meaning is implicit and it may be repeated, so a change has to be made in
several places.

Propose extracting a NAMED CONSTANT:

1. Introduce a `const` with a descriptive name whose value is the literal.
2. Replace every occurrence of the literal that means the same thing with the
   constant.
3. Place the constant at an appropriate scope (package-level if used by several
   functions, otherwise local to the function).
4. Do NOT change the numeric value — the program must compute exactly the same
   results.

Constraints:
- Only replace literals that genuinely share this meaning; leave unrelated
  occurrences of the same digits alone.
- Change only the affected code; do not touch unrelated functions.
- Return ONLY a unified diff. Do not include prose.

RATIONALE (surfaced to the reviewer): a named constant documents intent and gives
one place to change the value, but the NAME and the SCOPE are judgement calls,
and a too-eager replacement can fold together two literals that merely happen to
share digits. That is why this is PROPOSE-ONLY (auto_apply is false) and gated:
the verifier (compile + tests:affected + detector-clears) proves the value is
unchanged, the affected tests stay green, and no bare magic literal remains — a
verified extraction, reviewed before it lands.
