"""A module with one swallowed broad except and one correct narrow except.

`load` wraps a parse in `except Exception: pass`, swallowing every error with no
logging or re-raise; python-bare-except flags it (propose-only) and the llm fix
narrows the catch to the specific exception and handles it. `save` uses
`except ValueError as e:` and logs it, the correct pattern, so it is correctly NOT
flagged — proving the rule targets only the swallow-everything shapes.
"""


def load(raw):
    try:
        return parse(raw)
    except Exception:
        pass


def save(value):
    try:
        return write(value)
    except ValueError as e:
        log(e)
        raise
