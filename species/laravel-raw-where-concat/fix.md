# laravel-raw-where-concat fix prompt (LLM-assisted, SECURITY-stage)

The detected statement builds a RAW SQL fragment by concatenating a runtime value
into the SQL text — e.g. `->whereRaw("status = " . $status)` or
`DB::raw("count(" . $col . ")")`. The concatenated value is parsed as SQL, not
bound as data, so an attacker-controlled value can rewrite the statement: this is
a SQL-injection vulnerability. The remediation is to move every interpolated
value out of the SQL text into a BOUND parameter.

Fix it by parameterizing the raw fragment:

1. Replace each concatenated value with a `?` placeholder in the static SQL
   string, and pass the value(s) in the method's bindings array argument, in
   order: `->whereRaw("status = ?", [$status])`. `whereRaw`/`orWhereRaw`/
   `havingRaw`/`orderByRaw`/`selectRaw`/`groupByRaw` all accept a bindings array
   as their second argument.
2. If a concatenated fragment is an IDENTIFIER (a column/table name) rather than a
   value — which cannot be a bound parameter — prefer dropping `Raw` for the
   builder equivalent (e.g. `->where('status', $status)`, `->orderBy($column)`),
   and validate any dynamic identifier against an allow-list. Do NOT leave the
   identifier concatenated into raw SQL.
3. For `DB::raw(...)` used as a select/expression, move user values to bindings on
   the surrounding query method; keep `DB::raw` only for a STATIC SQL expression
   with no interpolated value.

Constraints:
- Change only the statement containing the finding; do not touch unrelated code.
- After the fix NO raw-SQL builder call may contain a string concatenation (`.`)
  of a value into its SQL text.
- Return ONLY a unified diff. Do not include prose.

This fix is staged for human review (auto_apply is false): which values bind and
whether a fragment is a value or an identifier is a security judgement. The
verifier gate (detector-clears + a `php -l` parse check) must pass: no raw call
built by concatenation may remain, and the file must still parse.
