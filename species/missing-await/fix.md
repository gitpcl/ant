# missing-await fix prompt (LLM-assisted, ADR-0002)

Go has no `await`. This species targets the closest Go analogue: a goroutine
spawned in a loop that is never waited on or synchronized (`go func(){...}()`
with no WaitGroup, channel, or errgroup). The enclosing function returns before
the goroutines finish, so their results are dropped, and any shared variable
they write is updated concurrently without coordination — a data race.

Fix it by awaiting and synchronizing the spawned work:

1. Collect each goroutine's result into a per-index slice
   (`results := make([]T, len(coll))`) so writes do not contend on one shared
   variable. Capture the loop index and value as goroutine parameters.
2. Add a `sync.WaitGroup`: `wg.Add(1)` before each `go`, `defer wg.Done()` as the
   goroutine's first statement, and `wg.Wait()` after the loop. (Add the `sync`
   import if it is not already present.) For fallible work, prefer
   `golang.org/x/sync/errgroup` and propagate the first error.
3. After `wg.Wait()`, aggregate the collected results into the original output
   exactly as the per-iteration body did.

Constraints:
- Change only the function containing the finding (plus the import block if a new
  import is needed); do not touch unrelated code.
- The post-fix code must be data-race free (the test suite runs with `-race`).
- Return ONLY a unified diff. Do not include prose.

This fix is staged for human review (auto_apply is false). The verifier gate
(compile + tests:affected + detector-clears) must pass: the post-fix code must
compile, be race-clean, keep the affected tests green, and the missing-await
detector must report zero matches (the loop body is no longer a bare,
un-synchronized `go func`).
