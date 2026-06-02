# laravel-env-call fix prompt (LLM-assisted)

The detected statement calls Laravel's `env('KEY')` helper from a file OUTSIDE
the `config/` directory. Once the application config is cached
(`php artisan config:cache`) — which every production deploy does — `env()`
returns null there, because env vars are only read while the cached config is
built. This is a latent production bug.

Fix it by reading the value through the config repository instead:

1. Replace `env('KEY', $default)` with `config('group.key', $default)`, where
   `group` is the relevant `config/*.php` file and `key` is the entry within it.
   Use the existing config key when one already maps to this env var; otherwise
   choose the conventional one (e.g. `env('MAIL_FROM')` → `config('mail.from')`).
2. Preserve the default value argument if one was present.
3. Do NOT move env() into config/ as part of this fix — the remediation is to
   stop calling env() at runtime outside config/, not to relocate the call.

Constraints:
- Change only the statement containing the finding; do not touch unrelated code.
- The post-fix behavior must be identical when the config is NOT cached and
  CORRECT (non-null) when it is.
- Return ONLY a unified diff. Do not include prose.

This fix is staged for human review (auto_apply is false): the right config key
is a judgement call. The verifier gate (detector-clears + a `php -l` parse check)
must pass: no env() call may remain outside config/, and the file must still
parse.
