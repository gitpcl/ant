package detect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gitpcl/ant/internal/engine"
)

// commandDetectTimeout bounds a single command-detector script invocation so a
// hung script surfaces as an operational error rather than stalling the scan.
// It mirrors the spirit of the harness adapters' per-task deadline (TECHSPEC
// §10): detection is a plugin boundary and must never hang the colony.
const commandDetectTimeout = 60 * time.Second

// commandDetector is the script escape-hatch Detector (TECHSPEC §2/§4
// detect/command.go): for smells that cannot be expressed as a single-file AST
// match, it execs a species-supplied script over the scope and parses the
// script's JSON output into engine.Findings. The canonical use is CROSS-FILE
// analysis the ast-grep adapter cannot do — e.g. cross-referencing a manifest's
// declared dependencies against the imports actually used across the tree
// (unused-dependency), or a CI job against the pipeline that references it
// (dead-config). The engine owns no analysis of its own; the script does the
// work and emits findings on a defined JSON contract, exactly as the ast-grep
// adapter parses ast-grep's JSON.
//
// SECURITY (Sprint 020): a command detector runs at SCAN time (`ant scout`),
// a broader exec surface than the Sprint-017 tool fixer (fix/verify time only).
// This adapter therefore NEVER resolves a shell — it execs the interpreter +
// script path in argv form, so a script path or scope value can never be
// interpolated into a shell command. The TRUST decision (may an untrusted /
// freshly-installed community species' script run at all?) is enforced UPSTREAM
// at the composition root (colony.buildDetector), which refuses to construct a
// command detector for an unreviewed OriginUser species. This adapter assumes
// its caller has already cleared that gate — it is the mechanism, not the policy.
type commandDetector struct {
	species string // species that owns the findings this detector produces

	// interp is the interpreter binary (e.g. "sh", "bash", "python3") resolved
	// from PATH at Detect time; script is the absolute/relative path the
	// interpreter runs. Both come from the manifest's [detector].command/script,
	// kept as separate argv elements so no shell string is ever built.
	interp string
	script string

	// timeout bounds a single invocation; zero uses commandDetectTimeout.
	timeout time.Duration

	// runner executes the command and returns stdout; injectable so tests
	// exercise the parse path with a recorded payload and the missing-binary
	// path without a live interpreter (mirrors astgrepDetector.runner).
	runner commandRunner
}

// compile-time assertion that the adapter satisfies the engine.Detector
// interface (TECHSPEC §5.1).
var _ engine.Detector = (*commandDetector)(nil)

// CommandOption configures a commandDetector.
type CommandOption func(*commandDetector)

// WithCommandRunner injects a command runner for tests; unexported callers use
// the production execRunner. Exported so a recorded-output test in another
// package can drive the parse path without a live interpreter.
func WithCommandRunner(r commandRunner) CommandOption {
	return func(d *commandDetector) {
		if r != nil {
			d.runner = r
		}
	}
}

// WithCommandTimeout overrides the per-invocation deadline (default
// commandDetectTimeout).
func WithCommandTimeout(t time.Duration) CommandOption {
	return func(d *commandDetector) {
		if t > 0 {
			d.timeout = t
		}
	}
}

// NewCommand builds a command (script escape-hatch) detector for a species. interp
// is the interpreter binary (resolved from PATH at Detect time) and script is the
// path it runs; both come from the manifest [detector].command/script. The binary
// is not probed until Detect runs, so a missing interpreter surfaces as an
// operational error on use, never a hard failure at startup (mirrors NewASTGrep).
func NewCommand(species, interp, script string, opts ...CommandOption) engine.Detector {
	d := &commandDetector{
		species: species,
		interp:  interp,
		script:  script,
		timeout: commandDetectTimeout,
		runner:  execRunner,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Detect runs the script over the scope and maps its JSON output to Findings.
// The script receives the scope root as its sole positional argument (argv[1]),
// so it analyzes the tree without the engine interpolating anything into a shell.
// A missing interpreter returns a typed *engine.DetectorUnavailableError (exit
// code 2 — TECHSPEC §7.1), never a panic; any other exec or parse failure is
// wrapped with context. A timeout is bounded by the adapter's deadline.
func (d *commandDetector) Detect(ctx context.Context, scope engine.Scope) ([]engine.Finding, error) {
	root := scope.Root
	if root == "" {
		root = "."
	}

	runCtx := ctx
	if d.timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, d.timeout)
		defer cancel()
	}

	// argv form ONLY: interpreter, script path, scope root. No shell, so neither
	// the script path nor the scope can be interpreted as a shell command.
	args := []string{d.script, root}
	out, err := d.runner(runCtx, d.interp, args)
	if err != nil {
		if cerr := runCtx.Err(); cerr != nil {
			return nil, fmt.Errorf("%w: detect: command script for species %q timed out: %v", engine.ErrOperational, d.species, cerr)
		}
		if isBinaryNotFound(err) {
			return nil, &engine.DetectorUnavailableError{
				Detector: "command",
				Binary:   d.interp,
				Err:      err,
			}
		}
		return nil, fmt.Errorf("detect: command script for species %q: %w", d.species, err)
	}
	return d.parseFindings(out)
}

// commandFinding is the JSON contract a command-detector script emits, one
// object per finding in a top-level array on stdout. It is a deliberately small,
// EXPLICIT subset of engine.Finding so a script in any language can produce it
// with a few fields, and it maps onto the SAME engine.Finding the ast-grep
// adapter produces — introducing NO new wire field (the --json/scout-json
// contract stays byte-unchanged for existing species; TECHSPEC §12). Positions
// are 1-based here (unlike ast-grep's 0-based), because a script author counts
// lines naturally from 1; the adapter passes them through unchanged.
//
//	[
//	  {
//	    "file":     "go.mod",          // path relative to the scope root (required)
//	    "line":     7,                 // 1-based start line (required)
//	    "endLine":  7,                 // 1-based end line (defaults to line)
//	    "col":      1,                 // 1-based start column (defaults to 1)
//	    "endCol":   42,                // 1-based end column (defaults to col)
//	    "severity": "medium",          // low|medium|high (defaults to medium)
//	    "message":  "…",               // human-readable finding (required)
//	    "snippet":  "require foo v1",  // the offending text (optional)
//	    "sourceLine":"\trequire foo v1",// VERBATIM source line(s) incl. indentation
//	                                     // (optional; feeds the deterministic fixer)
//	    "ruleId":   "unused-dependency" // detector-specific id, surfaced in meta
//	  }
//	]
//
// An empty stdout (or "[]") means no findings.
type commandFinding struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	EndLine    int    `json:"endLine"`
	Col        int    `json:"col"`
	EndCol     int    `json:"endCol"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
	Snippet    string `json:"snippet"`
	SourceLine string `json:"sourceLine"`
	RuleID     string `json:"ruleId"`
}

// parseFindings decodes the script's JSON array and maps each entry onto an
// engine.Finding owned by this species. A non-JSON or malformed payload is a
// wrapped error (a script that misbehaves fails loudly, never silently produces
// zero findings). Defaults fill in the optional fields so a minimal script
// (file+line+message) is valid.
func (d *commandDetector) parseFindings(out []byte) ([]engine.Finding, error) {
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return []engine.Finding{}, nil // no output: no findings, not nil
	}
	var raw []commandFinding
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return nil, fmt.Errorf("detect: parse command JSON for species %q: %w", d.species, err)
	}
	findings := make([]engine.Finding, 0, len(raw))
	for i, r := range raw {
		f, err := d.toFinding(r)
		if err != nil {
			return nil, fmt.Errorf("detect: command finding %d for species %q: %w", i, d.species, err)
		}
		findings = append(findings, f)
	}
	return findings, nil
}

// toFinding maps one script-emitted commandFinding onto an engine.Finding,
// validating the required fields and defaulting the optional ones. Positions are
// already 1-based (the script's natural counting), so they are passed through.
func (d *commandDetector) toFinding(r commandFinding) (engine.Finding, error) {
	if r.File == "" {
		return engine.Finding{}, fmt.Errorf("missing required \"file\"")
	}
	if r.Line <= 0 {
		return engine.Finding{}, fmt.Errorf("missing or invalid required \"line\" (want 1-based)")
	}
	if r.Message == "" {
		return engine.Finding{}, fmt.Errorf("missing required \"message\"")
	}

	endLine := r.EndLine
	if endLine <= 0 {
		endLine = r.Line
	}
	col := r.Col
	if col <= 0 {
		col = 1
	}
	endCol := r.EndCol
	if endCol <= 0 {
		endCol = col
	}

	sev := engine.SeverityMedium
	if r.Severity != "" {
		parsed, err := engine.ParseSeverity(r.Severity)
		if err != nil {
			return engine.Finding{}, fmt.Errorf("invalid severity %q (want low|medium|high)", r.Severity)
		}
		sev = parsed
	}

	return engine.Finding{
		Species: d.species,
		File:    r.File,
		Span: engine.Span{
			StartLine: r.Line,
			StartCol:  col,
			EndLine:   endLine,
			EndCol:    endCol,
		},
		Severity:    sev,
		Message:     r.Message,
		Snippet:     r.Snippet,
		SourceLines: r.SourceLine,
		Meta:        map[string]string{"ruleId": ruleIDOrSpecies(r.RuleID, d.species)},
	}, nil
}

// ruleIDOrSpecies returns the script-supplied ruleId, falling back to the species
// name so Finding.Meta["ruleId"] is always populated (consistent with the
// ast-grep adapter, which always sets ruleId).
func ruleIDOrSpecies(ruleID, species string) string {
	if ruleID != "" {
		return ruleID
	}
	return species
}
