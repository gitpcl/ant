# python-sql-fstring fix prompt (LLM-assisted, SECURITY-stage — Sprint 024)

The detected statement builds a SQL query by interpolating a runtime value into
an f-string passed to a DB call — e.g. `cur.execute(f"SELECT * FROM users WHERE
id = {user_id}")` or `text(f"... {value}")`. The interpolated value is parsed as
SQL, not bound as data, so an attacker who controls the value can rewrite the
statement (read or destroy data, bypass authentication): this is a SQL-injection
vulnerability.

Fix it by moving every interpolated value out of the SQL text into a BOUND
PARAMETER:

1. Replace the f-string with a PLAIN (non-`f`) string literal whose interpolated
   values become driver placeholders. Match the placeholder style the driver
   already uses elsewhere in the file/package: `?` (sqlite3), `%s` (psycopg /
   PyMySQL), or a named `:name` for SQLAlchemy `text(...)`. Do NOT keep the `f`
   prefix and do NOT leave any `{...}` substitution in the SQL text.
2. Pass the previously-interpolated value(s) in the call's params argument, in
   order: a tuple/list for positional placeholders
   (`cur.execute("... WHERE id = ?", (user_id,))`) or a dict for named ones
   (`conn.execute(text("... WHERE id = :id"), {"id": user_id})`).
3. Keep the same call (`execute` / `text`), the same selected columns, and the
   same downstream result handling — change ONLY how the value reaches the query.
4. If an interpolated fragment is an IDENTIFIER (a table/column name) rather than
   a value — which cannot be a bound parameter — validate it against an allow-list
   before composing it; do NOT leave it interpolated into the SQL text.

Constraints:
- Change only the statement containing the finding; do not touch unrelated code.
- After the fix NO `execute(...)` / `text(...)` call may contain an f-string with
  an interpolation — the static literal plus bound params must fully replace it
  (otherwise the injection vector remains).
- The post-fix query must return the same result as before for the same input.
- Return ONLY a unified diff. Do not include prose.

This fix is staged for human review (auto_apply is false): SQL injection is a
security change and the correct binding is a judgement call. The verifier gate
(detector-clears + a `python -m py_compile` parse check) must pass: no execute/
text call built by an interpolated f-string may remain, and the file must still
parse.
