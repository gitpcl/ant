# deep-nesting fix prompt (LLM-assisted, ADR-0002) — SIGNATURE (Sprint 019)

The detected function nests conditionals to the configured depth threshold
(default 3). Deeply nested `if` blocks bury the success path under indentation
and hide the exit conditions.

Flatten the nesting with GUARD CLAUSES / EARLY RETURNS while preserving behavior
EXACTLY:

1. For each outer condition guarding the success path, INVERT it and return the
   else/fall-through result early. `if cond { rest }` becomes
   `if !cond { return <fall-through> }` followed by `rest` un-indented.
2. Apply this top-down so the success path ends up at the bottom of the function
   with no indentation, and every early-exit condition is a single guard line.
3. Preserve the exact return values: the result on each inverted-guard path MUST
   equal the value the original nested code produced when that condition was
   false (here, the function's final fall-through `return`).
4. Do NOT change the success-path result, the function signature, or any code
   outside the flattened block.

Constraints:
- Behavior must be identical for every input: same return value on every path.
- Change only the function containing the finding; do not touch unrelated code.
- Return ONLY a unified diff. Do not include prose.

RATIONALE (surfaced to the reviewer): guard-clause flattening is the textbook
fix for arrow-code, but inverting conditions is error-prone (a wrong negation or
a mis-ordered guard silently changes behavior). That is exactly why this is
PROPOSE-ONLY (auto_apply is false) and gated: the verifier (compile +
tests:affected + detector-clears) proves the flattened function still compiles,
keeps the affected tests green, and that no depth-3 nest remains — so the
reviewer sees a *verified* refactor, not a hopeful one.
