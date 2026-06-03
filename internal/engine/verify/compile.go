package verify

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/langmap"
)

// CheckCompile is the canonical name of the compile check.
const CheckCompile = "compile"

// BuildCommand runs a project build/typecheck in dir and returns combined
// output plus an error on a non-zero exit. It is injectable so the verifier's
// scratch-tree + result logic is testable WITHOUT a real toolchain or the ant
// repo building itself (TECHSPEC §12 — CI needs no live build of a fixture it
// did not author). Each language registers one BuildCommand in the BuildTable;
// production uses goBuildCommand (`go build ./...`) for Go.
type BuildCommand func(ctx context.Context, dir string) (output []byte, err error)

// BuildTable maps a canonical language token (langmap.Go, langmap.PHP, …) to the
// BuildCommand that typechecks/lints that language in a scratch tree. A language
// with no row is UNSUPPORTED: the verifier emits an honest skip-with-reason
// ("no compile checker for <lang>"), never a vacuous pass (the core Sprint 026
// fix for the go-build-on-a-non-Go-repo hole). The table is the single per-
// language dispatch authority for compile, mirroring affected.go's runner table.
type BuildTable map[string]BuildCommand

// compileVerifier checks that a proposed diff still builds. It applies the diff
// to a SCRATCH COPY of the tree (never the real working tree), resolves the
// diff's language(s) via the single langmap authority, and runs the matching
// per-language builder there. A language with no registered builder is a VISIBLE
// skip-with-reason, never a silent green (TECHSPEC §5.3, Sprint 026). It does NOT
// lock: the colony serializes build-state verifiers behind the pool's per-project
// mutex (TECHSPEC §8.1), so adding a lock here would be redundant.
type compileVerifier struct {
	table BuildTable
}

// compile-time assertion that compileVerifier satisfies engine.Verifier.
var _ engine.Verifier = (*compileVerifier)(nil)

// NewCompile returns a compile verifier driven by a per-language BuildTable.
//
// Back-compat seam (KEPT for Sprint 026): a nil table falls back to a Go-only
// table whose builder is the real `go build ./...`, so existing callers and
// tests that wrote NewCompile(nil) keep their exact prior behavior. Tests inject
// a fake table (or use NewCompileFor) to stay hermetic.
func NewCompile(table BuildTable) engine.Verifier {
	if table == nil {
		table = BuildTable{langmap.Go: goBuildCommand}
	}
	return &compileVerifier{table: table}
}

// NewCompileFor is the back-compat seam for the OLD single-BuildCommand API: it
// builds a Go-only table from a single BuildCommand. A nil build uses the real
// `go build`. This preserves every existing `NewCompile(fakeBuild)` test (which
// injected a 2-arg fake) by routing it through the table as the Go builder, so
// a Go diff exercises the fake exactly as before.
func NewCompileFor(build BuildCommand) engine.Verifier {
	if build == nil {
		build = goBuildCommand
	}
	return &compileVerifier{table: BuildTable{langmap.Go: build}}
}

// defaultBuildTable is the production per-language compile dispatch table. Each
// builder is MISSING-BINARY TOLERANT: when the language's toolchain is absent the
// build returns an exec-not-found error that Verify converts into a CLEAN SKIP,
// so CI without php/node/tsc/vue-tsc/python stays green (Sprint 026 env note).
//   - go         → go build ./...        (whole-module typecheck)
//   - typescript → tsc --noEmit          (whole-project typecheck)
//   - vue        → vue-tsc --noEmit      (SFC-aware typecheck)
//   - php        → php -l <file>…        (lint each changed PHP file)
//   - python     → python -m py_compile  (bytecode-compile each changed file)
func defaultBuildTable() BuildTable {
	return BuildTable{
		langmap.Go:         goBuildCommand,
		langmap.TypeScript: wholeTreeBuilder("tsc", "--noEmit"),
		langmap.Vue:        wholeTreeBuilder("vue-tsc", "--noEmit"),
		langmap.PHP:        perFileBuilder(langmap.PHP, "php", []string{"-l"}, nil),
		langmap.Python:     perFileBuilder(langmap.Python, "python", []string{"-m", "py_compile"}, nil),
	}
}

// NewCompileDefault constructs the compile verifier with the production
// per-language table. This is the wiring used by the colony recipe (Sprint 026):
// a Go diff runs `go build`, a PHP diff runs `php -l`, an unsupported language is
// an honest skip.
func NewCompileDefault() engine.Verifier { return NewCompile(defaultBuildTable()) }

// Verify copies the scope root, applies the diff, resolves the diff's
// language(s), and runs each language's builder in the copy. A clean build is a
// pass; a non-zero exit fails WITH the build output as the detail (the skip
// reason is the actual compiler error). A language with NO registered builder is
// an honest skip-with-reason — NEVER a pass that hides an unchecked diff (the
// Sprint 026 regression guard). A missing toolchain binary is also a clean skip.
// A scratch-prep error is a failed check (a visible skip), never a panic.
func (v *compileVerifier) Verify(ctx context.Context, diff engine.ProposedDiff, scope engine.Scope) engine.VerifyResult {
	langs := diffLanguages(diff)
	if len(langs) == 0 {
		return passResult(CheckCompile, "no changed files to compile")
	}

	// Resolve which of the diff's languages have a registered builder. A language
	// with none is the honest-skip case: record it so the developer sees the diff
	// went unchecked rather than mistaking it for a green build.
	var supported []string
	var unsupported []string
	for _, l := range langs {
		if _, ok := v.table[l]; ok {
			supported = append(supported, l)
		} else {
			unsupported = append(unsupported, l)
		}
	}
	if len(supported) == 0 {
		return skipResult(CheckCompile, fmt.Sprintf("no compile checker for %s", strings.Join(langs, ", ")))
	}

	st, cleanup, err := newScratchTree(scope.Root, diff)
	if err != nil {
		return failResult(CheckCompile, fmt.Sprintf("could not prepare scratch tree: %v", err))
	}
	defer cleanup()

	for _, l := range supported {
		out, err := v.table[l](ctx, st.root)
		if err != nil {
			if isBinaryNotFound(err) {
				// Missing toolchain → clean skip, not a failure (CI without the
				// language's tools stays green — Sprint 026 env note).
				return skipResult(CheckCompile, fmt.Sprintf("%s compile checker binary not found", l))
			}
			detail := fmt.Sprintf("%s build failed: %v", l, err)
			if len(bytes.TrimSpace(out)) > 0 {
				detail = fmt.Sprintf("%s build failed: %s", l, bytes.TrimSpace(out))
			}
			return failResult(CheckCompile, detail)
		}
	}

	if len(unsupported) > 0 {
		// Some languages were checked, others have no checker. Pass (the checked
		// ones built), but surface the unchecked languages so it is honest.
		return passResult(CheckCompile, fmt.Sprintf("compiled %s; no checker for %s",
			strings.Join(supported, ", "), strings.Join(unsupported, ", ")))
	}
	return passResult(CheckCompile, "project builds cleanly after the diff")
}

// goBuildCommand is the production Go BuildCommand: `go build ./...` in dir,
// returning combined stdout+stderr so a build error reaches the CheckResult
// detail verbatim.
func goBuildCommand(ctx context.Context, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

// wholeTreeBuilder returns a BuildCommand that runs `binary args…` once in the
// scratch tree root (e.g. `tsc --noEmit`, `vue-tsc --noEmit`), the way
// go-build typechecks the whole module. A missing binary surfaces as an
// exec-not-found error so Verify converts it to a clean skip.
func wholeTreeBuilder(binary string, args ...string) BuildCommand {
	return func(ctx context.Context, dir string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = dir
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err := cmd.Run()
		return buf.Bytes(), err
	}
}

// perFileBuilder returns a BuildCommand that lints/compiles EACH changed file of
// the given language individually (e.g. `php -l <file>`, `python -m py_compile
// <file>`), which is how those linters work — one file per invocation. It walks
// the scratch tree for files whose langmap language matches `language`, then runs
// `binary preArgs… <file> postArgs…` per file. The first non-zero exit returns
// its output+error. A missing binary on the first invocation surfaces as
// exec-not-found so Verify converts it to a clean skip. With no matching files it
// is a no-op success (nothing of this language to check in the tree).
func perFileBuilder(language, binary string, preArgs, postArgs []string) BuildCommand {
	return func(ctx context.Context, dir string) ([]byte, error) {
		files, err := filesOfLanguage(dir, language)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			args := append(append(append([]string{}, preArgs...), f), postArgs...)
			cmd := exec.CommandContext(ctx, binary, args...)
			cmd.Dir = dir
			var buf bytes.Buffer
			cmd.Stdout = &buf
			cmd.Stderr = &buf
			if err := cmd.Run(); err != nil {
				return buf.Bytes(), err
			}
		}
		return nil, nil
	}
}

// filesOfLanguage walks dir and returns the paths (relative to dir, slash-form)
// of every regular file whose langmap language matches `language`. It skips
// nothing extra — the scratch tree is already scoped — but stays defensive: an
// unreadable entry aborts with the error so the builder reports it rather than
// silently skipping files.
func filesOfLanguage(dir, language string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if langmap.LanguageForPath(path) == language {
			rel, rerr := filepath.Rel(dir, path)
			if rerr != nil {
				return rerr
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
