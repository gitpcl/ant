# resource-leak fix prompt (LLM-assisted, ADR-0002) — SIGNATURE (Sprint 018)

The detected function opens a file/resource (`os.Open`) but never closes it on
ANY path — there is no `Close` call anywhere in the function. Every return path
leaks the file descriptor; under load this exhausts the process's descriptor
limit and the failure is far from the cause.

Fix it by closing the resource on ALL paths:

1. Immediately after the open succeeds (after its error check, so you do not
   close a nil/invalid handle), add `defer f.Close()`. `defer` guarantees the
   close runs on EVERY return path — the success path and every early-error path.
2. If the function also returns an error, consider surfacing a Close error on the
   success path (e.g. via a named return + deferred check) where it matters; at
   minimum the descriptor must always be released.
3. Do NOT reorder or change the existing logic beyond inserting the close.

Constraints:
- Change only the function containing the finding; do not touch unrelated code.
- The post-fix behavior must be identical on every path (the resource is now
  closed, but the returned values are unchanged for the same input).
- Return ONLY a unified diff. Do not include prose.

RATIONALE (surfaced to the reviewer): a leaked descriptor is a slow-motion
outage — it works in tests and fails in production once enough requests pile up,
and the stack trace points at an unrelated open elsewhere. A single
`defer f.Close()` after the open closes the resource on every path, including the
error returns the function already has. It is staged for human review (auto_apply
is false) because the close placement and whether a Close error must be reported
are judgement calls. The verifier gate (compile + tests:affected +
detector-clears) must pass: the rewrite must compile, keep the affected tests
green on both the success and error paths, and leave no os.Open-without-Close
function behind.
