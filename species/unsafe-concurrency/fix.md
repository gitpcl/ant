# unsafe-concurrency fix prompt (LLM-assisted, ADR-0002) — PREMIUM (Sprint 018)

The detected function spawns one or more goroutines that write SHARED state with
no synchronization — no mutex guards the concurrent writes and no WaitGroup (or
channel) owns the goroutines' lifecycle. The writes race (undefined behavior
under `go test -race`) and the function may read the shared state before the
goroutines finish.

Fix it by adding the missing synchronization:

1. Guard every write to the shared variable with a `sync.Mutex` (Lock/Unlock, or
   use `sync/atomic` if the state is a single counter).
2. Own the goroutines' lifecycle with a `sync.WaitGroup`: `wg.Add(1)` before each
   spawn, `defer wg.Done()` inside the goroutine, and `wg.Wait()` before the
   function reads the shared result.
3. Capture loop variables explicitly (pass them as goroutine arguments) so each
   goroutine sees its own value, not a shared loop variable.
4. Preserve the computed result exactly: after the fix the function must produce
   the same value it was intended to produce, now deterministically and race-free.

Constraints:
- Change only the function containing the finding; do not touch unrelated code.
- The post-fix result must be correct AND race-free under `go test -race`.
- Return ONLY a unified diff. Do not include prose.

RATIONALE (surfaced to the reviewer): unsynchronized concurrent writes are
undefined behavior — the program may work in testing and corrupt data in
production, and the bug is non-deterministic and hard to reproduce. Adding the
mutex makes the write safe; the WaitGroup makes the result observable. This is
the highest-risk class of change, so it is ALWAYS staged for human review
(auto_apply is false) even when the gate passes. The verifier gate (compile +
tests:affected + detector-clears) must pass, and CI runs the suite under
`go test -race`: the rewrite must compile, keep the affected tests green, be
race-free, and leave no unsynchronized `go func` behind.
