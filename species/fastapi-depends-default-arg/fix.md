# fastapi-depends-default-arg fix prompt (LLM-assisted)

The detected parameter has a MUTABLE default value (`= []` or `= {}`). In Python
the default object is created ONCE when the function is defined and SHARED across
every call, so mutations leak between invocations — a state-bleed bug that is
especially dangerous in a FastAPI handler or dependency, where each call serves a
different request.

Fix it with the standard sentinel pattern:

1. Change the parameter's default to `= None`.
2. At the top of the function body, initialize it: `items = items if items is
   not None else []` (or `{}` for a dict). Use the parameter's actual name.
3. If the parameter is a FastAPI dependency that was mistakenly given a bare
   mutable literal, give it a proper `Depends(...)` default instead.

Constraints:
- Change only the detected parameter and add the minimal in-body initializer; do
  not touch unrelated code.
- Preserve the function's behavior for every caller that passed a value.
- Return ONLY a unified diff. Do not include prose.

This fix is staged for human review (auto_apply is false): the right replacement
(sentinel-plus-init vs Depends) is a judgement call. The verifier gate
(detector-clears + a `python -m py_compile` parse check) must pass: no mutable
default may remain, and the file must still parse.
