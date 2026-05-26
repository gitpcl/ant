# duplicate-code-small fix prompt (LLM-assisted, ADR-0002) — Sprint 019

The detected block is duplicated across two functions: the same logic, copy-
pasted. Duplicated code drifts (a change applied to one copy and forgotten in the
other) and hides the shared intent.

Propose extracting a SHARED HELPER:

1. Create a new, well-named helper function containing the duplicated block,
   taking the value(s) it operates on as parameters and returning the result.
2. Replace each duplicated copy with a call to the helper.
3. Keep each caller's surrounding, non-duplicated logic intact (only the shared
   block moves).

Constraints:
- Behavior must be identical for every input at every call site: same return
  value, same side effects.
- Do not over-generalize — extract exactly the shared block, not unrelated code.
- Change only the affected functions and add the helper; do not touch unrelated
  code.
- Return ONLY a unified diff. Do not include prose.

RATIONALE (surfaced to the reviewer): DRYing up duplication is good hygiene, but
the helper's boundary and parameter list are judgement calls, and a careless
extraction can subtly change one caller's behavior. That is why this is
PROPOSE-ONLY (auto_apply is false) and gated: the verifier (compile +
tests:affected + detector-clears) proves the helper compiles, BOTH callers still
compute the same result, and no locally-computed duplicate remains — a verified
consolidation, reviewed before it lands.
