# unchecked-type-assertion fix prompt (LLM-assisted, ADR-0002)

The detected statement uses the single-result type-assertion form `v := x.(T)`.
If x's dynamic type is not T, this PANICS at runtime — there is no chance to
recover or report the mismatch.

Fix it by switching to the comma-ok form and handling the not-ok case:

1. Rewrite `v := x.(T)` to `v, ok := x.(T)`.
2. Handle the `!ok` branch in the way that fits the enclosing function:
   - If the function returns an `error`, return a clear error when `!ok`
     (e.g. `if !ok { return ..., fmt.Errorf("expected T, got %T", x) }`).
   - If it cannot return an error, guard so the invalid `v` is never used on the
     not-ok path (return a zero value, skip, or log) — never fall through.
3. Do NOT change the behavior when the assertion holds: when `ok` is true, the
   post-fix code must compute exactly the same result as before.

Constraints:
- Change only the function containing the finding; do not touch unrelated code.
- The post-fix result must be identical to the pre-fix result when the assertion holds.
- Return ONLY a unified diff. Do not include prose.

RATIONALE (surfaced to the reviewer): an unchecked assertion turns a type
mismatch — often a recoverable, reportable condition — into a hard panic that
takes down the goroutine. The comma-ok form makes the mismatch a value the code
can handle. It is staged for human review (auto_apply is false) because the
correct not-ok handling (error vs default vs skip) is a judgement call. The
verifier gate (compile + tests:affected + detector-clears) must pass: the rewrite
must compile, keep the affected tests green, and leave no `v := x.(T)` behind.
