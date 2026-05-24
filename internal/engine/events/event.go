// Package events is the colony event bus and the canonical event vocabulary.
// All state changes flow through the bus; the TUI renderer and the --json
// renderer are two consumers of the same stream (TECHSPEC §3, §8, §11). Each
// event therefore carries enough payload to drive both — it is the single
// source of truth for what happened during a run.
package events

import "github.com/gitpcl/ant/internal/engine"

// Type is the canonical event kind. The set matches TECHSPEC §11 exactly; the
// front doors (Claude Code skill, Pi extension, CI) depend on these strings, so
// they are a stable contract.
type Type string

const (
	TypeRunStart      Type = "run.start"
	TypeDetectFinding Type = "detect.finding"
	TypeAntStart      Type = "ant.start"
	TypeAntVerified   Type = "ant.verified"
	TypeAntSkipped    Type = "ant.skipped"
	TypeApplyDone     Type = "apply.done"
	TypeRunEnd        Type = "run.end"
)

// Event is one record on the bus. Type selects which payload field is
// populated; the others are nil. Seq is a monotonic per-bus sequence number so
// consumers can detect drops and assert ordering. The payload is split into
// typed pointers (rather than an any) so the --json renderer and the TUI both
// get a compile-checked shape with no type switches on interface{}.
type Event struct {
	Type Type   `json:"type"`
	Seq  int    `json:"seq"`
	Time string `json:"time"` // RFC3339; set by the publisher

	RunStart      *RunStartPayload      `json:"runStart,omitempty"`
	DetectFinding *DetectFindingPayload `json:"detectFinding,omitempty"`
	AntStart      *AntStartPayload      `json:"antStart,omitempty"`
	AntVerified   *AntVerifiedPayload   `json:"antVerified,omitempty"`
	AntSkipped    *AntSkippedPayload    `json:"antSkipped,omitempty"`
	ApplyDone     *ApplyDonePayload     `json:"applyDone,omitempty"`
	RunEnd        *RunEndPayload        `json:"runEnd,omitempty"`
}

// RunStartPayload announces a colony run. Carries the scope so the TUI can show
// what is being scanned and --json records the run parameters.
type RunStartPayload struct {
	RunID string       `json:"runId"`
	Scope engine.Scope `json:"scope"`
}

// DetectFindingPayload reports a finding as detection discovers it, feeding the
// work queue. The full Finding is included so renderers need no back-reference.
type DetectFindingPayload struct {
	RunID   string         `json:"runId"`
	Finding engine.Finding `json:"finding"`
}

// AntStartPayload marks an ant picking up a finding. AntID identifies the
// worker so the TUI can render per-ant lanes; Finding ties it to the work item.
type AntStartPayload struct {
	RunID   string         `json:"runId"`
	AntID   int            `json:"antId"`
	Finding engine.Finding `json:"finding"`
}

// AntVerifiedPayload marks a fix that passed verification and was staged. It
// carries the diff (provenance + files) and the verify result (which checks
// passed) so review and --json can show the full trust chain.
type AntVerifiedPayload struct {
	RunID  string              `json:"runId"`
	AntID  int                 `json:"antId"`
	Diff   engine.ProposedDiff `json:"diff"`
	Verify engine.VerifyResult `json:"verify"`
}

// AntSkippedPayload marks a fix discarded because a required verifier failed.
// FailedCheck names the gate that failed and Reason gives the detail — a skip
// is a trust signal that must be visible, never a silent drop (PRD §6.3).
type AntSkippedPayload struct {
	RunID       string              `json:"runId"`
	AntID       int                 `json:"antId"`
	Finding     engine.Finding      `json:"finding"`
	FailedCheck engine.CheckResult  `json:"failedCheck"`
	Reason      string              `json:"reason"`
	Verify      engine.VerifyResult `json:"verify"`
}

// ApplyDonePayload reports a staged diff landed by `ant apply`. Branch is the
// branch it landed on (empty for --no-branch); Commit is the resulting hash.
type ApplyDonePayload struct {
	RunID  string `json:"runId"`
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	Commit string `json:"commit,omitempty"`
}

// RunEndPayload closes a run with aggregate counts for the summary line and the
// CI exit-code decision (highest severity seen vs --fail-on). Error is set when
// the run aborted on an operational failure (e.g. a missing detector binary);
// it is the empty string for a clean run. Carrying it on the terminal event
// keeps the --json stream well-formed (it always ends with run.end) and makes
// the failure visible to the front doors that parse it (TECHSPEC §11, §12).
type RunEndPayload struct {
	RunID           string `json:"runId"`
	Findings        int    `json:"findings"`
	Verified        int    `json:"verified"`
	Skipped         int    `json:"skipped"`
	Applied         int    `json:"applied"`
	HighestSeverity string `json:"highestSeverity"`
	Error           string `json:"error,omitempty"`
}
