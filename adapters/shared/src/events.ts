/**
 * Typed mirror of Ant's `--json` event contract (TECHSPEC §12), frozen and
 * golden-tested on the Go side at internal/engine/events/event.go and
 * internal/engine/scout/testdata/scout-json.golden. These types are PARSED by
 * the front doors; they do NOT define the contract — the Go engine does. Keep
 * the field names in lockstep with the Go json tags. If the golden changes,
 * this file changes; never the other way around.
 *
 * The front doors are thin shells: they exec `ant <verb> --json` and decode this
 * stream. No detection, fixing, trust, or orchestration logic lives here — that
 * is all in the Go binary (TECHSPEC §3 engine/front-door seam).
 */

/** The canonical event kinds, matching events.Type in event.go exactly. */
export const EVENT_TYPES = [
  'run.start',
  'detect.finding',
  'ant.start',
  'ant.verified',
  'ant.skipped',
  'apply.done',
  'run.end',
] as const;

export type EventType = (typeof EVENT_TYPES)[number];

/** engine.Severity renders as one of these lowercase tokens. */
export type Severity = 'low' | 'medium' | 'high';

/** engine.Span — 1-based half-open code region. */
export interface Span {
  startLine: number;
  startCol: number;
  endLine: number;
  endCol: number;
}

/** engine.Finding — a located, read-only observation from a Detector. */
export interface Finding {
  species: string;
  file: string;
  span: Span;
  severity: Severity;
  message: string;
  snippet: string;
  meta?: Record<string, string>;
}

/** engine.FileDiff — a unified-diff patch for one file. */
export interface FileDiff {
  path: string;
  patch: string;
}

/** engine.ProposedDiff — a Fixer's output plus provenance. */
export interface ProposedDiff {
  files: FileDiff[];
  fixer: string;
  rationale?: string;
}

/** engine.CheckResult — one verifier check's outcome. */
export interface CheckResult {
  name: string;
  passed: boolean;
  detail?: string;
}

/** engine.VerifyResult — aggregate verifier outcome. */
export interface VerifyResult {
  passed: boolean;
  checks: CheckResult[];
}

/** engine.Scope — the bounds of a run. */
export interface Scope {
  root: string;
  paths?: string[];
  species?: string[];
  ignoreGlobs?: string[];
}

/** Payloads mirror the *Payload structs in event.go. */
export interface RunStartPayload {
  runId: string;
  /**
   * Machine-readable version of the --json event-stream contract
   * (events.SchemaVersion on the Go side). Present on every run.start — the
   * first event of every stream — so a front door can detect a breaking
   * contract change at stream open. Adding fields does NOT bump it; renaming or
   * removing an existing field does. See internal/engine/events/event.go.
   */
  schemaVersion: string;
  scope: Scope;
}

export interface DetectFindingPayload {
  runId: string;
  finding: Finding;
}

export interface AntStartPayload {
  runId: string;
  antId: number;
  finding: Finding;
}

export interface AntVerifiedPayload {
  runId: string;
  antId: number;
  diff: ProposedDiff;
  verify: VerifyResult;
}

export interface AntSkippedPayload {
  runId: string;
  antId: number;
  finding: Finding;
  failedCheck: CheckResult;
  reason: string;
  verify: VerifyResult;
}

export interface ApplyDonePayload {
  runId: string;
  path: string;
  branch?: string;
  commit?: string;
}

export interface RunEndPayload {
  runId: string;
  findings: number;
  verified: number;
  skipped: number;
  applied: number;
  highestSeverity: string;
  error?: string;
}

/**
 * AntEvent is one record on the stream. `type` selects which payload field is
 * populated; the rest are absent. This is a discriminated union so consumers
 * narrow on `type` and get the right payload with no manual casts — exactly the
 * shape the Go side encodes (one populated pointer per event).
 */
export type AntEvent =
  | (Base & { type: 'run.start'; runStart: RunStartPayload })
  | (Base & { type: 'detect.finding'; detectFinding: DetectFindingPayload })
  | (Base & { type: 'ant.start'; antStart: AntStartPayload })
  | (Base & { type: 'ant.verified'; antVerified: AntVerifiedPayload })
  | (Base & { type: 'ant.skipped'; antSkipped: AntSkippedPayload })
  | (Base & { type: 'apply.done'; applyDone: ApplyDonePayload })
  | (Base & { type: 'run.end'; runEnd: RunEndPayload });

interface Base {
  seq: number;
  time: string;
}

/** isEventType narrows an arbitrary string to a known EventType. */
export function isEventType(s: string): s is EventType {
  return (EVENT_TYPES as readonly string[]).includes(s);
}
