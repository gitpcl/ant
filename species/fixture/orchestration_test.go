package fixture_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gitpcl/ant/species/fixture"
)

// writeFakeTool writes an executable shell script tool on a fresh dir and
// prepends that dir to PATH for the test. It is the deterministic FAKE formatter/
// autofixer the Sprint 017 orchestration fixtures run instead of a real
// gofmt/prettier/ruff/eslint — CI must NOT depend on those being installed
// (sprint contract). The tool-runner (fix) and formatter-idempotence (verify)
// resolve it from PATH exactly as they resolve a real tool.
func writeFakeTool(t *testing.T, name, scriptBody string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("orchestration fixtures use a POSIX shell-script fake tool; skipped on Windows")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" + scriptBody + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tool %q: %v", name, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// stripTrailingWS is a fake gofmt/prettier stand-in: it strips trailing
// whitespace from each line in place. Run as `<tool> -w <file>`, it edits the
// file the way an in-place formatter does. On already-clean input a second run is
// a no-op, so it is IDEMPOTENT — the formatter-idempotence gate passes.
const stripTrailingWS = `f="$2"; sed 's/[[:space:]]*$//' "$f" > "$f.tmp" && mv "$f.tmp" "$f"`

// stripTrailingWSLastArg is the same trailing-whitespace stripper as
// stripTrailingWS, but it reads the file from the LAST positional argument
// instead of "$2". The PHP tool fakes (pint, php-cs-fixer) are invoked as
// `pint {file}` / `php-cs-fixer fix {file}`, so the scratch path is the final
// argument, not a fixed "$2" slot. Using the last arg lets one fake body serve
// both invocation shapes. On already-clean input a second run is a no-op, so it
// is IDEMPOTENT and the formatter-idempotence gate passes.
const stripTrailingWSLastArg = `eval "f=\${$#}"; sed 's/[[:space:]]*$//' "$f" > "$f.tmp" && mv "$f.tmp" "$f"`

// sortImportLines is a fake goimports/isort stand-in: it sorts the contiguous
// block of import spec lines inside a Go `import ( ... )` group alphabetically.
// Sorting an already-sorted block changes nothing, so it is IDEMPOTENT and the
// formatter-idempotence gate passes.
const sortImportLines = `f="$2"
awk '
  /^import \($/ { print; insort=1; n=0; next }
  insort==1 && /^\)$/ {
    for (i=1;i<=n;i++) for (j=i+1;j<=n;j++) if (a[j]<a[i]) { t=a[i]; a[i]=a[j]; a[j]=t }
    for (i=1;i<=n;i++) print a[i]
    insort=0; print; next
  }
  insort==1 { a[++n]=$0; next }
  { print }
' "$f" > "$f.tmp" && mv "$f.tmp" "$f"`

// TestOrchestrationSpeciesFixtures runs the detect→fix→verify→golden harness over
// each Sprint 017 tool-runner species, pointing the tool-runner fix AND the
// formatter-idempotence verifier at a FAKE formatter on PATH (Case.ToolCommand
// override) so the genuine pipeline runs without a real formatter installed.
func TestOrchestrationSpeciesFixtures(t *testing.T) {
	t.Run("formatter-drift", func(t *testing.T) {
		writeFakeTool(t, "fakefmt", stripTrailingWS)
		fixture.RunCase(t, fixture.Case{
			Name:        "formatter-drift",
			SpeciesDir:  filepath.Join("..", "formatter-drift"),
			RepoDir:     filepath.Join("testdata", "formatter-drift", "repo"),
			GoldenPath:  filepath.Join("testdata", "formatter-drift", "fix.golden"),
			ToolCommand: "fakefmt",
			ToolArgs:    []string{"-w", fixture.PlaceholderFile},
		})
	})

	t.Run("import-sort", func(t *testing.T) {
		writeFakeTool(t, "fakeimports", sortImportLines)
		fixture.RunCase(t, fixture.Case{
			Name:        "import-sort",
			SpeciesDir:  filepath.Join("..", "import-sort"),
			RepoDir:     filepath.Join("testdata", "import-sort", "repo"),
			GoldenPath:  filepath.Join("testdata", "import-sort", "fix.golden"),
			ToolCommand: "fakeimports",
			ToolArgs:    []string{"-w", fixture.PlaceholderFile},
		})
	})

	t.Run("lint-autofix", func(t *testing.T) {
		writeFakeTool(t, "fakelint", stripTrailingWS)
		fixture.RunCase(t, fixture.Case{
			Name:        "lint-autofix",
			SpeciesDir:  filepath.Join("..", "lint-autofix"),
			RepoDir:     filepath.Join("testdata", "lint-autofix", "repo"),
			GoldenPath:  filepath.Join("testdata", "lint-autofix", "fix.golden"),
			ToolCommand: "fakelint",
			ToolArgs:    []string{"--fix", fixture.PlaceholderFile},
		})
	})

	// pint-format (Sprint 023 PHP wave): templated on import-sort/formatter-drift.
	// The detector nominates a class marked `// ant:pint-format`; the tool-runner
	// runs a FAKE `pint` (the stripTrailingWS stand-in — a real Pint would do the
	// same trailing-whitespace normalization) over the whole file, and the SAME
	// fake re-runs as the formatter-idempotence gate (no further change = converged).
	// No compile/tests:affected gate — on a non-Go repo that is a vacuous Go-build
	// pass (sprint-023 contract); formatter-idempotence is the genuine proof. The
	// fixture file is valid PHP so a real `php -l` would parse it.
	t.Run("pint-format", func(t *testing.T) {
		writeFakeTool(t, "pint", stripTrailingWSLastArg)
		fixture.RunCase(t, fixture.Case{
			Name:        "pint-format",
			SpeciesDir:  filepath.Join("..", "pint-format"),
			RepoDir:     filepath.Join("testdata", "pint-format", "repo"),
			GoldenPath:  filepath.Join("testdata", "pint-format", "fix.golden"),
			ToolCommand: "pint",
			ToolArgs:    []string{fixture.PlaceholderFile},
		})
	})

	// php-cs-fixer (Sprint 023 PHP wave): templated on formatter-drift. It SHIPS
	// DISABLED by default (species.toml enabled=false) because it overlaps Pint —
	// a project enables one or the other. The harness drives the detect→fix→verify
	// path regardless of the runtime enabled flag (enabled=false is a resolution-
	// time concern); TestPHPCSFixerShipsDisabled asserts it resolves disabled. Same
	// formatter-idempotence-only gate as pint-format.
	t.Run("php-cs-fixer", func(t *testing.T) {
		writeFakeTool(t, "php-cs-fixer", stripTrailingWS)
		fixture.RunCase(t, fixture.Case{
			Name:        "php-cs-fixer",
			SpeciesDir:  filepath.Join("..", "php-cs-fixer"),
			RepoDir:     filepath.Join("testdata", "php-cs-fixer", "repo"),
			GoldenPath:  filepath.Join("testdata", "php-cs-fixer", "fix.golden"),
			ToolCommand: "php-cs-fixer",
			ToolArgs:    []string{"fix", fixture.PlaceholderFile},
		})
	})

	// ruff-format (Sprint 024 Python wave): templated on formatter-drift/pint-format.
	// The detector nominates a function marked `# ant:ruff-format`; the tool-runner
	// runs a FAKE `ruff` (the stripTrailingWSLastArg stand-in — a real `ruff format`
	// would do the same trailing-whitespace normalization) over the whole file, and
	// the SAME fake re-runs as the formatter-idempotence gate (no further change =
	// converged). No compile/tests:affected gate — on a non-Go repo that is a vacuous
	// Go-build pass (sprint-024 contract); formatter-idempotence is the genuine proof.
	// The fixture file is valid Python so a real `python -m py_compile` would parse it.
	t.Run("ruff-format", func(t *testing.T) {
		writeFakeTool(t, "ruff", stripTrailingWSLastArg)
		fixture.RunCase(t, fixture.Case{
			Name:        "ruff-format",
			SpeciesDir:  filepath.Join("..", "ruff-format"),
			RepoDir:     filepath.Join("testdata", "ruff-format", "repo"),
			GoldenPath:  filepath.Join("testdata", "ruff-format", "fix.golden"),
			ToolCommand: "ruff",
			ToolArgs:    []string{"format", fixture.PlaceholderFile},
		})
	})

	// ruff-autofix (Sprint 024 Python wave): templated on lint-autofix. The detector
	// nominates a function marked `# ant:ruff-autofix`; the tool-runner runs a FAKE
	// `ruff` (the stripTrailingWSLastArg stand-in for `ruff check --fix`, reading the
	// scratch path from the LAST arg since {file} trails the subcommand flags), and
	// the same fake re-runs as the formatter-idempotence gate. This species ALSO
	// declares a `command:verify.sh` py_compile gate — RunCase runs the species'
	// declared verifier chain (formatter-idempotence + command:verify.sh) against the
	// scratch tree, so the py_compile parse check runs for real here (python3 is a
	// host tool, not faked). No compile/tests:affected (vacuous Go pass, sprint-024).
	t.Run("ruff-autofix", func(t *testing.T) {
		writeFakeTool(t, "ruff", stripTrailingWSLastArg)
		fixture.RunCase(t, fixture.Case{
			Name:          "ruff-autofix",
			SpeciesDir:    filepath.Join("..", "ruff-autofix"),
			RepoDir:       filepath.Join("testdata", "ruff-autofix", "repo"),
			GoldenPath:    filepath.Join("testdata", "ruff-autofix", "fix.golden"),
			ToolCommand:   "ruff",
			ToolArgs:      []string{"check", "--fix", fixture.PlaceholderFile},
			RequiredTools: []string{"python3"},
		})
	})

	// black-format (Sprint 024 Python wave): templated on formatter-drift. It SHIPS
	// DISABLED by default (species.toml enabled=false) because it overlaps ruff-format
	// — a project enables one or the other. The harness drives the detect→fix→verify
	// path regardless of the runtime enabled flag (enabled=false is a resolution-time
	// concern); TestEmbed_BuiltinsDiscoverableNoDisk asserts it resolves disabled. Same
	// formatter-idempotence-only gate as ruff-format.
	t.Run("black-format", func(t *testing.T) {
		writeFakeTool(t, "black", stripTrailingWSLastArg)
		fixture.RunCase(t, fixture.Case{
			Name:        "black-format",
			SpeciesDir:  filepath.Join("..", "black-format"),
			RepoDir:     filepath.Join("testdata", "black-format", "repo"),
			GoldenPath:  filepath.Join("testdata", "black-format", "fix.golden"),
			ToolCommand: "black",
			ToolArgs:    []string{fixture.PlaceholderFile},
		})
	})

	// isort-imports (Sprint 024 Python wave): templated on import-sort. The detector
	// nominates a function marked `# ant:isort-imports`; the tool-runner runs a FAKE
	// import organizer (the stripTrailingWSLastArg stand-in for `ruff check --select I
	// --fix`, reading the scratch path from the LAST arg)
	// and the same fake re-runs as the formatter-idempotence gate. No
	// compile/tests:affected (vacuous Go pass, sprint-024). The fixture is valid Python.
	t.Run("isort-imports", func(t *testing.T) {
		writeFakeTool(t, "ruff", stripTrailingWSLastArg)
		fixture.RunCase(t, fixture.Case{
			Name:        "isort-imports",
			SpeciesDir:  filepath.Join("..", "isort-imports"),
			RepoDir:     filepath.Join("testdata", "isort-imports", "repo"),
			GoldenPath:  filepath.Join("testdata", "isort-imports", "fix.golden"),
			ToolCommand: "ruff",
			ToolArgs:    []string{"check", "--select", "I", "--fix", fixture.PlaceholderFile},
		})
	})
}
