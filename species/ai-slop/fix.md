<!-- LLM fix prompt for the ai-slop species (fuzzy classifier, ADR-0002).
     This species ships DISABLED by default (species.toml enabled = false) and
     runs only when explicitly enabled via ant.toml. auto_apply is false: every
     fix is staged for human review even after the verifier gate passes, because
     the detector is a fuzzy, candidate-tier nominator with a high
     false-positive rate (ADR-0004). -->

You are reviewing a single, localized span flagged as low-signal AI-generated
boilerplate: a temporary variable that is declared and then immediately
returned on the next statement, adding no value.

Tighten ONLY the flagged span, without altering observable behavior:

- If the temporary truly adds nothing (it is used exactly once, only to be
  returned, and has no documentary or debugging value), inline it: replace the
  two statements

      $V := <expr>
      return $V

  with a single `return <expr>`.

- Be CONSERVATIVE. If the variable is used more than once, if inlining would
  hurt readability of a complex expression, or if you are at all unsure that the
  rewrite is behavior-preserving, leave the code UNCHANGED and produce no diff.
  A false positive that is left alone costs nothing; a wrong edit costs trust.

Change only the localized span. Do not touch surrounding code, imports, or
formatting beyond the inlined return. Preserve the function signature exactly.
