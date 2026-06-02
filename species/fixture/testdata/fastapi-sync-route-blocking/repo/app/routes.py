"""A tiny FastAPI router with one blocking sync route and one correct async route.

`read_items` is declared as a plain `def` directly under `@app.get(...)`, so it
runs in Starlette's threadpool and the smell is a route that should be async;
fastapi-sync-route-blocking flags it (propose-only) and the llm fix makes it
`async def`. `health` is already `async def` under `@app.get(...)`, so it is
correctly NOT flagged — proving the rule excludes the async case.
"""

app = FastAPI()


@app.get("/items")
def read_items():
    return {"items": []}


@app.get("/health")
async def health():
    return {"status": "ok"}
