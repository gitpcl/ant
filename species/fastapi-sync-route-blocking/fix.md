# fastapi-sync-route-blocking fix prompt (LLM-assisted)

The detected function is a FastAPI route handler declared as a PLAIN `def` (not
`async def`) directly under an `@app.<method>` / `@router.<method>` route
decorator. FastAPI runs a single asyncio event loop; a route that should be async
but is declared sync, or that performs slow work on the request path, degrades
throughput for every concurrent request.

Fix it by making the handler asynchronous:

1. Change the handler's `def` to `async def`.
2. If the body calls async-capable APIs (an async DB client, httpx.AsyncClient,
   etc.), `await` them.
3. If the body does genuinely blocking work (a sync DB driver, a CPU-bound
   computation) that cannot be awaited, offload it instead of inlining it — e.g.
   wrap it with `from starlette.concurrency import run_in_threadpool` and
   `await run_in_threadpool(blocking_call, ...)`.

Constraints:
- Change only the detected route handler; do not touch unrelated code.
- Preserve the handler's parameters, return value, and observable behavior.
- Keep the route decorator and path unchanged.
- Return ONLY a unified diff. Do not include prose.

This fix is staged for human review (auto_apply is false): whether to simply mark
the handler async or to offload blocking work is a judgement call. The verifier
gate (detector-clears + a `python -m py_compile` parse check) must pass: no plain
`def` may remain under a route decorator, and the file must still parse.
