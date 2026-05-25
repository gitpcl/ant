# nil-deref fix prompt (LLM-assisted, ADR-0002)

The detected code discards the error from a fallible call with the blank
identifier (`v, _ := call(...)`) and then uses `v`, which may be nil on the
error path — a nil dereference waiting to panic.

Fix it by handling the error instead of discarding it:

1. Bind the second result to `err` instead of `_` (`v, err := call(...)`).
2. Immediately after the call, add an idiomatic guard: `if err != nil { return
   ..., err }`, returning the zero values for the function's other results
   alongside the error.
3. If the enclosing function does not already return an `error`, add `error` as
   its last result and return `nil` on the success path.
4. Update the success-path return to include the trailing `nil` error.

Constraints:
- Change only the function containing the finding; do not touch unrelated code.
- Preserve the existing behavior on the non-error (non-nil) path exactly.
- Return ONLY a unified diff. Do not include prose.

This fix is staged for human review (auto_apply is false). The verifier gate
(compile + tests:affected + detector-clears) must pass: the post-fix code must
compile, keep the affected tests green, and the nil-deref detector must report
zero matches (the `_` is gone, so the pattern no longer matches).
