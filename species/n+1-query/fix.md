# n+1-query fix prompt (LLM-assisted, ADR-0002)

The detected loop issues one lookup/query call per iteration (`v :=
lookupX(item)` inside `for ... := range coll`) — the N+1 query pattern: N rows
cause N+1 round trips.

Fix it by batching the per-iteration call into a single query before the loop:

1. Hoist the lookup out of the loop into ONE batched call that takes the whole
   collection and returns all rows at once (e.g. `rows := lookupXs(coll)`). If a
   batched variant does not exist, call the existing one once over the full set.
2. Rewrite the loop to range over the pre-fetched results instead of calling per
   item (`for _, row := range rows { ... }`).
3. Preserve the original ordering of the produced output exactly.
4. Preserve all existing error handling; if the batched call returns an error,
   propagate it the same way the original per-item call's errors were handled.

Constraints:
- Change only the function containing the finding; do not touch unrelated code.
- The post-fix result must be identical to the pre-fix result for the same input.
- Return ONLY a unified diff. Do not include prose.

This fix is staged for human review (auto_apply is false). The verifier gate
(compile + tests:affected + detector-clears) must pass: the post-fix code must
compile, keep the affected tests green, and the n+1-query detector must report
zero matches (no per-iteration call remains inside the loop).
