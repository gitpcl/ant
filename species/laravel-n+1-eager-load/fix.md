# laravel-n+1-eager-load fix prompt (LLM-assisted)

The detected `foreach` iterates an Eloquent collection and accesses a RELATION on
each row (e.g. `$user->posts`). Because the relation is not eager-loaded, Laravel
issues one extra query per row — the N+1 query pattern: N rows cause N+1 round
trips.

Fix it by eager-loading the relation BEFORE the loop:

1. Add `->with('relation')` to the query that built the collection (e.g.
   `User::all()` → `User::with('posts')->get()`), or call `$collection->load(
   'relation')` before the loop when the set is already materialized.
2. Keep the loop body computing exactly the same result; the only change is that
   the relation is now pre-loaded, so the per-row access hits memory, not the DB.
3. Preserve the original ordering and any existing filtering.

Constraints:
- Change only the method containing the finding; do not touch unrelated code.
- The post-fix result must be identical to the pre-fix result for the same data.
- Return ONLY a unified diff. Do not include prose.

This fix is staged for human review (auto_apply is false). The verifier gate
(detector-clears + a `php -l` parse check) must pass: the per-row lazy-access
pattern must no longer match, and the file must still parse.
