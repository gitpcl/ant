# laravel-mass-assignment fix prompt (LLM-assisted, SECURITY-stage)

The detected statement performs an Eloquent mass-assignment from UNVALIDATED
request input — `Model::create($request->all())` or `$model->fill($request->all())`.
`$request->all()` returns EVERY field the client sent, so a request can set
columns the form never exposed (`is_admin`, `role`, account fields). This is the
overposting / privilege-escalation vulnerability. The remediation is to assign
only validated, whitelisted input.

Fix it by narrowing the argument:

1. PREFER `$request->validated()` when the controller action type-hints a
   FormRequest (or otherwise validates the request): it returns only the
   rule-checked subset, so unexpected fields cannot be written.
2. OTHERWISE pass an EXPLICIT whitelist array of exactly the fields this write is
   meant to set, reading each from the request:
   `['title' => $request->input('title'), 'body' => $request->input('body')]`.
   Choose the fields from the surrounding context (the model, the form, nearby
   code) — never re-introduce `->all()`.
3. Preserve the surrounding call exactly — only the argument changes. Do not
   alter the model class, the `create`/`fill` choice, or the assignment target.

Constraints:
- Change only the statement containing the finding; do not touch unrelated code.
- The fixed code must NOT pass `$request->all()` / `request()->all()` anywhere.
- Return ONLY a unified diff. Do not include prose.

This fix is staged for human review (auto_apply is false): which fields are safe
to assign is a security judgement. The verifier gate (detector-clears + a `php -l`
parse check) must pass: no create/fill fed by request input may remain, and the
file must still parse.
