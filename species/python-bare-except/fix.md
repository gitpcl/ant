# python-bare-except fix prompt (LLM-assisted)

The detected `try` block uses a broad exception handler that swallows errors: a
bare `except:` (which also catches KeyboardInterrupt and SystemExit, breaking
Ctrl-C and shutdown) or an `except Exception: pass` (which silently discards every
ordinary error). Either way a real failure becomes silent, undebuggable wrong
behavior.

Fix it by narrowing and handling:

1. Replace the bare/broad clause with one (or more) `except <SpecificError>:`
   clauses naming the exception(s) the `try` body can actually raise (e.g.
   `except (ValueError, KeyError) as e:`).
2. HANDLE the exception — do not leave a bare `pass`. At minimum log it
   (`logger.exception(...)`) and then either re-raise (`raise`), return a typed
   error/fallback, or convert it to the appropriate response.
3. If you genuinely cannot narrow the type, catch `except Exception as e:` (NEVER
   bare `except:`) and still log + re-raise rather than swallowing it.

Constraints:
- Change only the detected handler; do not touch the `try` body or unrelated code.
- Preserve the surrounding control flow.
- Return ONLY a unified diff. Do not include prose.

This fix is staged for human review (auto_apply is false): the right exception
type and handling are a judgement call. The verifier gate (detector-clears + a
`python -m py_compile` parse check) must pass: no bare `except:` or
`except Exception: pass` may remain, and the file must still parse.
