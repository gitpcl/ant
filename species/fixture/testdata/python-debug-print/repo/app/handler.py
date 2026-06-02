"""A small module with one stray debug print and one legitimate use of print.

The bare `print("DEBUG: ...")` statement below is ad-hoc debug output left in the
source; python-debug-print flags it (propose-only) and the delete-match fix removes
exactly that line. The `str(print)` reference further down uses `print` as a value
(not a statement-level call), so it is correctly NOT matched — proving the rule
targets the standalone debug STATEMENT, not every mention of print.
"""


def handle(name):
    print("DEBUG: handle called with", name)
    label = str(print)
    return label + ":" + name.strip()
