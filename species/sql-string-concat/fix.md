# sql-string-concat fix prompt (LLM-assisted, ADR-0002) — SECURITY (Sprint 018)

The detected statement builds a SQL query by CONCATENATING a runtime value into
the query text (`db.Query("SELECT ... WHERE id = " + value)`). The concatenated
value is interpreted as SQL, not as data — this is a SQL-injection vulnerability:
an attacker who controls the value can rewrite the statement (read or destroy
data, bypass authentication, escalate access).

Fix it by moving every interpolated value out of the query text into a BOUND
PARAMETER:

1. Replace each concatenated value in the query string with a placeholder the
   driver binds (`?` for MySQL/SQLite, `$1`/`$2`/… for PostgreSQL — match the
   placeholder style already used elsewhere in the file/package).
2. Make the query string a single static string literal with NO `+`
   concatenation and NO runtime value spliced in.
3. Pass the previously-concatenated value(s), in order, as trailing arguments to
   the same call (`db.Query("SELECT ... WHERE id = ?", value)`), so the database
   driver binds them as data and never interprets them as SQL.
4. Keep the same call (Query / QueryRow / Exec), the same selected columns, and
   the same result handling — change ONLY how the value reaches the query.

Constraints:
- Change only the statement(s) containing the finding; do not touch unrelated code.
- The post-fix query must return the same result as before for the same input.
- Do NOT keep any concatenation in the query text — the static literal plus bound
  parameters must fully replace it (otherwise the injection vector remains).
- Return ONLY a unified diff. Do not include prose.

RATIONALE (surfaced to the reviewer): concatenating a value into SQL text is a
SQL-injection risk — the value is parsed as part of the statement, so a crafted
input (`1; DROP TABLE users; --`, `' OR '1'='1`) changes what the query does, not
just what it returns. Bound parameters close the hole: the driver sends the static
query and the values separately, so the value can never be interpreted as SQL.
This fix is staged for human review (auto_apply is false) because it is a security
change and the correct binding is a judgement call. The verifier gate (compile +
tests:affected + detector-clears) must pass: the rewrite must compile, keep the
affected tests green, and leave no concatenated SQL string behind.
