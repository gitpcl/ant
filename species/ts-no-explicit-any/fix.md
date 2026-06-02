# ts-no-explicit-any fix prompt (LLM-assisted)

The detected annotation is an explicit `any`. `any` disables type checking for
that value: every property access, call, and assignment on it is unchecked, so a
real type error (a typo, a wrong shape, a renamed field) compiles cleanly and
fails only at runtime.

Fix it by replacing `any` with the precise type:

1. Infer the intended type from how the value is produced and used (its
   initializer, the function's other parameters/return, the call sites). Use the
   concrete type or interface when it is knowable (e.g. `User`, `string[]`,
   `Record<string, number>`).
2. When the type is genuinely dynamic/unknown, prefer `unknown` over `any` and
   narrow with a type guard before use — `unknown` keeps the value type-checked.
3. Do NOT widen unrelated code or change runtime behavior; only the annotation
   (and any guard the narrowing requires) changes.

Constraints:
- Change only the declaration containing the finding (and a minimal guard if
  `unknown` requires one); do not touch unrelated code.
- The post-fix code must `tsc --noEmit` cleanly — the narrowed type must be sound.
- Return ONLY a unified diff. Do not include prose.

This fix is staged for human review (auto_apply is false): the precise type is a
judgement call. The verifier gate (detector-clears + a `tsc --noEmit` parse/type
check) must pass: no `any` annotation may remain, and the file must type-check.
