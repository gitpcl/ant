// Package engine is the Ant colony library: the data types, the four core
// interfaces (Detector, Fixer, Verifier, Store), and the seams the CLI and the
// future enterprise layer build on. All business logic lives here; cmd/ant only
// parses flags, calls the engine, and renders (TECHSPEC §3).
package engine

// Span is a half-open code region: from (StartLine, StartCol) up to
// (EndLine, EndCol). Lines and columns are 1-based to match editor and
// ast-grep conventions.
type Span struct {
	StartLine int `json:"startLine"`
	StartCol  int `json:"startCol"`
	EndLine   int `json:"endLine"`
	EndCol    int `json:"endCol"`
}

// Finding is a single located issue reported by a Detector. Detectors never
// modify code; a Finding is a read-only observation (TECHSPEC §5.1).
type Finding struct {
	Species  string            `json:"species"` // species that owns this finding
	File     string            `json:"file"`
	Span     Span              `json:"span"` // start/end line+col
	Severity Severity          `json:"severity"`
	Message  string            `json:"message"`
	Snippet  string            `json:"snippet"`        // the localized code span
	Meta     map[string]string `json:"meta,omitempty"` // detector-specific extras
}

// CodeContext carries the code surrounding a finding, supplied to a Fixer so it
// can produce a localized diff without re-reading the whole file (TECHSPEC §5.2).
type CodeContext struct {
	File     string `json:"file"`
	Language string `json:"language"`
	Span     Span   `json:"span"`    // the finding's span within the file
	Snippet  string `json:"snippet"` // the localized code span
	Before   string `json:"before"`  // lines immediately preceding the span
	After    string `json:"after"`   // lines immediately following the span
}

// FixTask is one unit of work handed to a Fixer: a single finding plus the code
// context, and (for LLM-assisted fixers only) a prompt. Each task is
// independent — adapters are stateless between tasks (TECHSPEC §10).
type FixTask struct {
	Finding Finding     `json:"finding"`
	Context CodeContext `json:"context"`
	Prompt  string      `json:"prompt,omitempty"` // populated for LLM-assisted fixes only
}

// FileDiff is a unified-diff patch for a single file. Patch holds the diff body
// in standard unified-diff form so apply (go-git) and review can consume it.
type FileDiff struct {
	Path  string `json:"path"`
	Patch string `json:"patch"`
}

// ProposedDiff is a Fixer's output: a set of per-file diffs plus provenance.
// It is staged (never written to the working tree directly) until applied
// (TECHSPEC §5.2, §8).
type ProposedDiff struct {
	Files     []FileDiff `json:"files"`
	Fixer     string     `json:"fixer"`               // e.g. "pi (qwen2.5-coder)" — provenance
	Rationale string     `json:"rationale,omitempty"` // populated for the `explain` action
}

// CheckResult is one verifier check's outcome, retained for provenance so
// `ant review` and --json can show exactly why a diff passed or was skipped
// (TECHSPEC §5.3).
type CheckResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

// VerifyResult aggregates a verifier's checks. Passed is true only when every
// required check passed; a fix whose VerifyResult.Passed is false is skipped
// and never applied (TECHSPEC §5.3, §8).
type VerifyResult struct {
	Passed bool          `json:"passed"`
	Checks []CheckResult `json:"checks"`
}

// Scope bounds a run: the root path, the species to consider, and ignore
// globs. Detectors and verifiers operate within a Scope (TECHSPEC §8 step 1).
type Scope struct {
	Root        string   `json:"root"`              // working-tree root for the run
	Paths       []string `json:"paths,omitempty"`   // explicit paths; empty means whole root
	Species     []string `json:"species,omitempty"` // enabled species; empty means all enabled
	IgnoreGlobs []string `json:"ignoreGlobs,omitempty"`
}

// Run is the persisted record of a single colony invocation. The Store rounds a
// Run trip to disk so state survives process restarts and so the enterprise
// service-backed Store can plug into the same shape (TECHSPEC §5.4).
type Run struct {
	ID         string    `json:"id"`
	StartedAt  string    `json:"startedAt"`            // RFC3339 timestamp
	FinishedAt string    `json:"finishedAt,omitempty"` // RFC3339; empty while in progress
	Scope      Scope     `json:"scope"`
	Findings   []Finding `json:"findings,omitempty"`
}
