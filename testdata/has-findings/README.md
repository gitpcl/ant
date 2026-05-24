# has-findings fixture

A tiny fixture repo with deliberately detectable issues, used by the scout and
`--json` tests (Sprint 002).

- `main.go` line 3: unused import `strings` (maps to the `unused-import` match in
  `testdata/astgrep-output.json`, severity high).
- `main.go` line 6: `x := 1` assigned-but-effectively-dead (maps to the
  `dead-code` match, severity medium).

The line/column positions in `testdata/astgrep-output.json` are recorded against
this file so the detector adapter can be tested without a live `ast-grep` binary.
Scout must never modify any file in this directory — the non-mutation test
snapshots the tree before and after a run and asserts it is byte-identical.
