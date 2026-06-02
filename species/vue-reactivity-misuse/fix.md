# vue-reactivity-misuse fix prompt (LLM-assisted)

The detected line destructures a `reactive()` object in a Vue 3 `<script setup>`
block — `const { a, b } = state` where `state` was declared `const state =
reactive(...)`. Destructuring a reactive proxy copies the CURRENT primitive
values out of it, severing reactivity: the destructured locals never update when
the reactive state changes, a silent and common Vue 3 bug.

Fix it so reactivity is preserved:

1. Prefer `toRefs()`: `const { a, b } = toRefs(state)` returns refs that stay
   linked to the reactive source. Add `toRefs` to the existing `from 'vue'`
   import. Update the usages to `.value` where the destructured names are read in
   `<script>` (template usage of a ref unwraps automatically).
2. If `toRefs` does not fit, drop the destructure and access the fields through
   the proxy directly (`state.a`, `state.b`).
3. Do NOT change what the component renders or computes — only how the reactive
   fields are accessed.

Constraints:
- Edit only the `<script setup>` of the component containing the finding; do not
  touch unrelated code or the template beyond what the access change requires.
- The post-fix `<script setup>` must type-check.
- Return ONLY a unified diff against the .vue file. Do not include prose.

This fix is staged for human review (auto_apply is false): `toRefs` vs direct
proxy access is a judgement call. The verifier gate (detector-clears + a Vue
type-check) must pass: no `reactive()` destructure may remain, and the script
must type-check.
