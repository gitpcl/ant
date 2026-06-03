// Package detect holds the Detector adapters. Detection is a plugin boundary,
// not a build dependency (TECHSPEC §2): adapters shell out to external matchers
// (ast-grep, semgrep, eslint) or run a script escape hatch, then parse a defined
// JSON contract into []engine.Finding. The engine never embeds a matcher of its
// own.
package detect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/langmap"
)

// defaultASTGrepBinary is the executable the adapter shells out to. ast-grep is
// distributed as both `ast-grep` and the short alias `sg`; we use the canonical
// long name and let PATH resolution find it.
const defaultASTGrepBinary = "ast-grep"

// astgrepDetector shells out to the `ast-grep` binary, runs a rule file over a
// scope, and parses ast-grep's JSON match output into engine.Findings. It owns
// no detection logic itself — ast-grep does the AST matching (TECHSPEC §2,
// §5.1). The binary is resolved from PATH at Detect time, never at construction,
// so a missing binary surfaces as an operational error on use rather than a
// hard failure at startup.
type astgrepDetector struct {
	binary  string // executable name or path; defaults to "ast-grep"
	species string // species that owns the findings this detector produces
	rule    string // path to the ast-grep rule file (detect.yml)

	// globs are ast-grep `--globs` INCLUDE patterns scoping the scan to the
	// species' declared languages (Sprint 026). Derived from manifest.Languages
	// via the single langmap authority, so a `languages = ["php"]` species scans
	// only *.php and ast-grep never walks unrelated trees (vendor/, node_modules/)
	// it would otherwise descend into. Empty = scan everything (the prior
	// behavior — a species with no declared languages is unscoped).
	globs []string

	// runner executes the command and returns combined behavior; injectable so
	// tests exercise the parse path with a recorded payload and the
	// missing-binary path without a live binary.
	runner commandRunner
}

// commandRunner abstracts the exec call so the parse logic is testable against a
// recorded ast-grep payload (no live binary needed in CI, per the contract).
type commandRunner func(ctx context.Context, binary string, args []string) (stdout []byte, err error)

// compile-time assertion that the adapter satisfies the engine.Detector
// interface (TECHSPEC §5.1 / §5) AND the ScanSafeDetector marker: ast-grep runs
// no species-supplied script, so it is safe on the read-only `ant scout` path
// (unlike the command detector, which is deliberately NOT scan-safe).
var (
	_ engine.Detector         = (*astgrepDetector)(nil)
	_ engine.ScanSafeDetector = (*astgrepDetector)(nil)
)

// ScanSafe marks the ast-grep detector as safe to run on the read-only scout
// path: it shells out only to the vetted ast-grep matcher, never to a
// species-supplied script (engine.ScanSafeDetector, Sprint 020 defense-in-depth).
func (d *astgrepDetector) ScanSafe() bool { return true }

// Option configures an astgrepDetector.
type Option func(*astgrepDetector)

// WithBinary overrides the ast-grep executable name or path (default
// "ast-grep").
func WithBinary(name string) Option {
	return func(d *astgrepDetector) {
		if name != "" {
			d.binary = name
		}
	}
}

// withRunner injects a command runner for tests; unexported so production code
// always uses the real exec runner.
func withRunner(r commandRunner) Option {
	return func(d *astgrepDetector) {
		if r != nil {
			d.runner = r
		}
	}
}

// WithLanguages scopes the scan to a species' declared languages (Sprint 026).
// Each language is mapped to its file extensions via the single langmap
// authority and turned into an ast-grep `--globs` include pattern (e.g. php →
// "**/*.php"), so ast-grep walks only files of those languages and skips
// unrelated trees (vendor/, node_modules/) entirely — a correctness and
// performance fix for multi-stack repos. An empty/unknown language list leaves
// the scan UNSCOPED (the prior behavior), so Go species and any species without a
// `languages` field are unchanged.
func WithLanguages(languages []string) Option {
	return func(d *astgrepDetector) {
		var globs []string
		seen := make(map[string]bool)
		for _, lang := range languages {
			for _, ext := range langmap.ExtensionsFor(lang) {
				pattern := "**/*" + ext
				if !seen[pattern] {
					seen[pattern] = true
					globs = append(globs, pattern)
				}
			}
		}
		sort.Strings(globs)
		d.globs = globs
	}
}

// NewASTGrep builds an ast-grep detector for a species and its rule file. The
// rule file is the species' detect.yml (TECHSPEC §6.1). The binary is not
// probed until Detect runs.
func NewASTGrep(species, rule string, opts ...Option) engine.Detector {
	d := &astgrepDetector{
		binary:  defaultASTGrepBinary,
		species: species,
		rule:    rule,
		runner:  execRunner,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Detect runs ast-grep with the configured rule over the scope and maps every
// match to a Finding. A missing ast-grep binary returns a typed
// *engine.DetectorUnavailableError (maps to exit code 2 — TECHSPEC §7.1), never
// a panic. Any other exec or parse failure is wrapped with context.
func (d *astgrepDetector) Detect(ctx context.Context, scope engine.Scope) ([]engine.Finding, error) {
	args := d.buildArgs(scope)
	out, err := d.runner(ctx, d.binary, args)
	if err != nil {
		// A missing binary is an operational condition (exit 2), not a crash.
		if isBinaryNotFound(err) {
			return nil, &engine.DetectorUnavailableError{
				Detector: "ast-grep",
				Binary:   d.binary,
				Err:      err,
			}
		}
		return nil, fmt.Errorf("detect: ast-grep run for species %q: %w", d.species, err)
	}
	return d.parseMatches(out)
}

// buildArgs assembles the ast-grep scan invocation: a rule file, JSON stream
// output, and the scoped paths (the root when no explicit paths are given). The
// `scan` subcommand applies rule files; `--json=stream` emits one JSON object
// per line, but we request the default array form for a single decode.
func (d *astgrepDetector) buildArgs(scope engine.Scope) []string {
	args := []string{"scan", "--rule", d.rule, "--json"}
	// Scope to the species' declared languages (Sprint 026): one `--globs` include
	// pattern per registered extension, so ast-grep skips unrelated trees
	// (vendor/, node_modules/) instead of walking the whole tree. Empty globs =
	// unscoped (unchanged behavior).
	for _, g := range d.globs {
		args = append(args, "--globs", g)
	}
	targets := scope.Paths
	if len(targets) == 0 && scope.Root != "" {
		targets = []string{scope.Root}
	}
	return append(args, targets...)
}

// parseMatches decodes ast-grep's JSON match array and maps each match to a
// Finding, converting ast-grep's 0-based line/column to Ant's 1-based Span.
func (d *astgrepDetector) parseMatches(out []byte) ([]engine.Finding, error) {
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return []engine.Finding{}, nil // no matches: empty, not nil
	}
	var matches []astGrepMatch
	if err := json.Unmarshal(trimmed, &matches); err != nil {
		return nil, fmt.Errorf("detect: parse ast-grep JSON for species %q: %w", d.species, err)
	}
	findings := make([]engine.Finding, 0, len(matches))
	for _, m := range matches {
		findings = append(findings, d.toFinding(m))
	}
	return findings, nil
}

// toFinding maps one ast-grep match to an engine.Finding. The owning species
// comes from the adapter (a rule file belongs to exactly one species); ast-grep
// severity tokens map to the engine's three levels.
func (d *astgrepDetector) toFinding(m astGrepMatch) engine.Finding {
	return engine.Finding{
		Species: d.species,
		File:    m.File,
		Span: engine.Span{
			// ast-grep positions are 0-based; Ant Spans are 1-based.
			StartLine: m.Range.Start.Line + 1,
			StartCol:  m.Range.Start.Column + 1,
			EndLine:   m.Range.End.Line + 1,
			EndCol:    m.Range.End.Column + 1,
		},
		Severity: mapSeverity(m.Severity),
		Message:  m.Message,
		Snippet:  m.Text,
		// SourceLines is ast-grep's `lines`: the full source line(s) the match
		// covers WITH indentation, so a deterministic delete/rewrite fix can patch
		// lines that byte-match the working tree (lifting the column-0-only limit of
		// using the indentation-stripped `text`). Replacement is the per-match
		// `replacement` produced by a rule's `fix:` block; it is empty when the rule
		// declares no fix. Both are omitempty on the Finding, so the --json contract
		// is byte-unchanged for rules that capture neither (TECHSPEC §12).
		SourceLines: m.Lines,
		Replacement: m.Replacement,
		Meta:        map[string]string{"ruleId": m.RuleID},
	}
}

// mapSeverity translates ast-grep severity tokens to engine.Severity. ast-grep
// uses error/warning/info/hint; Ant collapses these onto high/medium/low. An
// unrecognized token falls back to medium so a finding is never silently
// dropped to an unknown severity.
func mapSeverity(token string) engine.Severity {
	switch token {
	case "error":
		return engine.SeverityHigh
	case "warning":
		return engine.SeverityMedium
	case "info", "hint":
		return engine.SeverityLow
	default:
		return engine.SeverityMedium
	}
}

// astGrepMatch mirrors the relevant subset of ast-grep's RuleMatchJSON output
// (camelCase fields). Only the fields Ant maps to a Finding are decoded; the
// rest (charCount, metaVariables, labels, metadata) are ignored. `lines` carries
// the full source line(s) with indentation; `replacement` is present only when
// the rule has a `fix:` block (the suggested new text for the matched span).
type astGrepMatch struct {
	Text        string       `json:"text"`
	Range       astGrepRange `json:"range"`
	File        string       `json:"file"`
	Lines       string       `json:"lines"`
	Replacement string       `json:"replacement"`
	RuleID      string       `json:"ruleId"`
	Severity    string       `json:"severity"`
	Message     string       `json:"message"`
}

type astGrepRange struct {
	Start astGrepPos `json:"start"`
	End   astGrepPos `json:"end"`
}

type astGrepPos struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// execRunner is the production commandRunner: it runs the binary and returns
// stdout. On a non-zero exit it returns the stderr text as the error so
// ast-grep's own diagnostics reach the caller.
func execRunner(ctx context.Context, binary string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if isBinaryNotFound(err) {
			return nil, err // preserved so Detect can type it
		}
		// ast-grep exits non-zero when matches are found in some modes; surface
		// stderr so the caller can distinguish a real failure from match-found.
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("%w: %s", err, bytes.TrimSpace(stderr.Bytes()))
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

// isBinaryNotFound reports whether err indicates the executable could not be
// located/started (the missing-binary case the contract requires us to handle
// gracefully). exec returns *exec.Error (wrapping exec.ErrNotFound) when the
// binary is not on PATH.
func isBinaryNotFound(err error) bool {
	if err == nil {
		return false
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return true
	}
	return errors.Is(err, exec.ErrNotFound)
}
