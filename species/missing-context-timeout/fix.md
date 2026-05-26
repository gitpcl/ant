# missing-context-timeout fix prompt (LLM-assisted, ADR-0002)

The detected call passes `context.Background()` directly into a blocking
network/DB operation. `context.Background()` never cancels and carries no
deadline, so the call has no upper bound — a slow or hung dependency stalls the
caller indefinitely.

Fix it by deriving a context with a timeout and passing that instead:

1. Before the call, derive a bounded context from the background context:
   `ctx, cancel := context.WithTimeout(context.Background(), <timeout>)` and
   `defer cancel()` immediately after, so the context is always released.
2. Pass `ctx` into the call in place of the inline `context.Background()`.
3. If the enclosing function already RECEIVES a `context.Context`, prefer
   threading that request-scoped context (optionally wrapped with WithTimeout)
   rather than starting from Background.
4. Choose a sensible default timeout (e.g. a few seconds) and leave it visible so
   a reviewer can tune it.

Constraints:
- Change only the function containing the finding; do not touch unrelated code.
- The post-fix behavior must be identical on the success path (the call still
  runs the same way, now under a deadline).
- Return ONLY a unified diff. Do not include prose.

RATIONALE (surfaced to the reviewer): a call with no deadline is a latent
availability bug — one unresponsive dependency can pin a goroutine (and its
resources) forever, and the failure only shows up under load. Adding a timeout
makes the call fail fast and recoverably. It is staged for human review
(auto_apply is false) because the timeout value and whether to thread an existing
context are opinionated choices. The verifier gate (compile + tests:affected +
detector-clears) must pass: the rewrite must compile, keep the affected tests
green, and leave no direct `context.Background()` at the call site.
