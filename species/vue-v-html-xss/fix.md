# vue-v-html-xss fix prompt (LLM-assisted, SECURITY stage)

The detected line uses Vue's `v-html` directive in a component template:
`v-html="expr"`. `v-html` renders `expr` as **raw, unsanitized HTML** directly
into the DOM. If any part of `expr` is influenced by user input, persisted data,
or any other untrusted source, this is a stored/reflected **cross-site scripting
(XSS)** sink — an attacker-supplied `<script>`/`<img onerror=…>` payload executes
in the victim's session. Vue's own documentation calls `v-html` out as dangerous
for exactly this reason.

Remediate the XSS sink. Choose the safest option that preserves intent:

1. **If the value is plain text** (no intentional markup): replace `v-html` with
   `v-text` or `{{ }}` interpolation, both of which HTML-escape the value. This is
   the strongest fix — escaped text can never inject markup.
   - `<div v-html="msg" />` → `<div>{{ msg }}</div>` (or `<div v-text="msg" />`).
2. **If the value is genuinely HTML that must render** (e.g. rich text the product
   intends to show): wrap it in a sanitizer before binding — do NOT bind raw input.
   Use an established sanitizer (e.g. DOMPurify): bind
   `v-html="sanitize(expr)"` where `sanitize` calls `DOMPurify.sanitize(expr)`,
   adding the import and a small computed/helper as needed. Never sanitize with a
   hand-rolled regex.
3. Keep the rendered text/markup intent unchanged — only close the injection vector.

Constraints:
- Edit only the template (and the minimal `<script>` needed to add a sanitizer
  import/helper if option 2 is chosen); do not touch unrelated code.
- After the fix, NO raw `v-html` binding may remain on an unsanitized value — the
  verifier's grep-clears gate requires that no `v-html` directive survives in any
  template (so prefer option 1 / a wrapped-sanitizer expression that the directive
  no longer reads raw input; if you must keep `v-html` for sanitized rich text,
  ensure it binds only the sanitizer's output).
- The post-fix SFC must type-check (`vue-tsc --noEmit` when present).
- Return ONLY a unified diff against the .vue file. Do not include prose.

This fix is staged for human review (auto_apply is false, SECURITY stage):
escaping vs sanitizing is a judgement call about whether the value is meant to be
markup. The verifier gate (detector-clears + grep-clears / a Vue type-check) must
pass: no raw `v-html` XSS sink may remain, and the SFC must stay well-formed.
