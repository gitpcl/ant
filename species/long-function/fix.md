# long-function fix prompt (LLM-assisted, ADR-0002) — Sprint 019

The detected function exceeds the configured statement threshold (default 6). A
long function is doing too much and hides its structure.

Propose a MECHANICAL extraction that preserves behavior EXACTLY:

1. Identify a cohesive run of statements that computes an intermediate result
   used by the rest of the function.
2. Extract it into a new, well-named helper function that takes the values it
   reads as parameters and returns the value(s) the caller needs.
3. Replace the extracted block in the original function with a single call to
   the helper, binding its result.
4. Extract enough that BOTH the original function AND the new helper end up below
   the threshold (do not just move the bulk into one oversized helper).

Constraints:
- Behavior must be identical for every input: same return value, same side
  effects in the same order.
- Do not change the original function's signature or its callers.
- Change only the function containing the finding and add the new helper; do not
  touch unrelated code.
- Return ONLY a unified diff. Do not include prose.

RATIONALE (surfaced to the reviewer): extraction is the standard remedy for long
functions, but WHERE to cut is a design decision and a careless split can change
evaluation order or capture the wrong variables. That is why this is PROPOSE-ONLY
(auto_apply is false) and gated: the verifier (compile + tests:affected +
detector-clears) proves the extracted code compiles, keeps the affected tests
green, and that neither the original nor the helper still exceeds the threshold —
the reviewer sees a verified extraction, not a guess.
