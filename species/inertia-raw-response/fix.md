# inertia-raw-response fix prompt (LLM-assisted)

The detected statement returns a RAW JSON response (`res.json(...)` /
`response.json(...)`) from an Inertia controller action. Inertia actions are
meant to return an Inertia page response via `Inertia.render('Page', props)` (or
the framework's `inertia()` helper). Returning bare JSON bypasses Inertia: the
browser receives JSON instead of a page visit, breaking SPA navigation and the
shared-layout/props contract.

Fix it by rendering the Inertia page instead:

1. Replace `return res.json(data)` with `return Inertia.render('Page/Name',
   props)`, where `'Page/Name'` is the Vue page component this action drives and
   `props` is the data the page expects (the object that was passed to
   `res.json(...)`).
2. Use the page name the surrounding routes/imports indicate; if it is not
   determinable from context, choose the conventional `Resource/Action` name
   (e.g. a `show` action → `'Users/Show'`).
3. Add the `Inertia` import if it is not already present, matching the project's
   import style.

Constraints:
- Change only the action containing the finding (plus the import if needed); do
  not touch unrelated code.
- The post-fix code must `tsc --noEmit` cleanly.
- Return ONLY a unified diff. Do not include prose.

This fix is staged for human review (auto_apply is false): the correct page name
and prop shape are judgement calls. The verifier gate (detector-clears + a
`tsc --noEmit` check) must pass: no raw res.json() return may remain in the
controller, and the file must type-check.
