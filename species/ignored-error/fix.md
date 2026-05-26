# ignored-error fix prompt (LLM-assisted, ADR-0002) — FLAGSHIP (Sprint 018)

The detected statement discards a function's error result into the blank
identifier (`v, _ := call()`). The error from `call` is silently swallowed, so
the code proceeds even when the call failed and `v` may be invalid — the most
common Go correctness bug.

Fix it by binding and handling the error at the call site:

1. Replace the `_` with a named `err` binding (`v, err := call()`).
2. Handle `err` in the way that fits the enclosing function:
   - If the function returns an `error`, propagate it (`if err != nil { return ..., err }`),
     wrapping with context where it clarifies the failure (`fmt.Errorf("...: %w", err)`).
   - If the function cannot return an error, log it or guard against the failure
     so the invalid `v` is never used on the error path.
3. Do NOT change the success-path behavior: when `err == nil`, the post-fix code
   must compute exactly the same result as before for the same input.
4. Preserve `v`'s existing usage on the success path.

Constraints:
- Change only the function containing the finding; do not touch unrelated code.
- The post-fix result must be identical to the pre-fix result on the success path.
- Return ONLY a unified diff. Do not include prose.

RATIONALE (surfaced to the reviewer): a discarded error means a failure that the
program never sees — the bug is invisible until the invalid `v` causes a wrong
result or a downstream panic. This fix makes the failure legible at the call
site. It is staged for human review (auto_apply is false) because the right
handling (propagate vs log vs default) is a judgement call. The verifier gate
(compile + tests:affected + detector-clears) must pass: the rewrite must compile,
keep the affected tests green, and leave no `v, _ := call()` discard behind.
