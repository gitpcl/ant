"""A handler with one mutable default argument and one correct default.

`list_items` declares `tags = []` — a shared mutable default that leaks state
across calls; fastapi-depends-default-arg flags it (propose-only) and the llm fix
rewrites it to the `= None` sentinel plus an in-body initializer. `get_page`
declares `page: int = 1`, a safe immutable default, so it is correctly NOT
flagged — proving the rule targets only the shared-mutable case.
"""


def list_items(tags = []):
    tags.append("default")
    return tags


def get_page(page: int = 1):
    return page
