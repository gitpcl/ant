"""A data-access module with one f-string SQL call and two safe calls.

`get_user` builds its query with an f-string interpolating `user_id` straight into
the SQL text (`cur.execute(f"... = {user_id}")`), so the value is parsed as SQL —
the SQL-injection smell python-sql-fstring flags (propose-only). The llm fix moves
the value to a bound `?` placeholder with a params tuple.

`count_active` passes a plain (non-f) string literal, and `get_order` passes an
already-parameterized `(sql, params)` pair, so both are correctly NOT flagged —
proving the rule targets only an interpolated f-string and leaves bound queries
alone.
"""


def get_user(cur, user_id):
    cur.execute(f"SELECT name FROM users WHERE id = {user_id}")
    return cur.fetchone()


def count_active(cur):
    cur.execute("SELECT count(*) FROM users WHERE active = 1")
    return cur.fetchone()


def get_order(cur, order_id):
    sql = "SELECT total FROM orders WHERE id = ?"
    cur.execute(sql, (order_id,))
    return cur.fetchone()
