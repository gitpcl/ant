/**
 * Pure presentation helpers over a parsed event stream. This is the line where
 * "thin shell" ends: it reshapes events the engine already produced into a
 * render-ready summary. It makes NO decisions the engine owns (no trust, no
 * severity thresholds, no fix logic) — it only counts and labels what the
 * stream already says happened, so each front door renders consistently.
 */

import type { AntEvent, Finding } from './events.ts';

export interface RunSummary {
  runId: string;
  root: string;
  findings: Finding[];
  verifiedCount: number;
  skippedCount: number;
  appliedCount: number;
  /** Skips with their reason — a skip is a trust signal, surfaced not hidden. */
  skips: { reason: string; check: string; finding: Finding }[];
  highestSeverity: string;
  /** Set when the run aborted on an operational error (run.end.error). */
  error?: string;
}

/**
 * summarize folds a parsed, well-formed stream into a RunSummary. It trusts the
 * stream's own run.end aggregate counts (the engine is the source of truth) and
 * collects the per-finding/per-skip detail the front doors render.
 */
export function summarize(events: AntEvent[]): RunSummary {
  const summary: RunSummary = {
    runId: '',
    root: '',
    findings: [],
    verifiedCount: 0,
    skippedCount: 0,
    appliedCount: 0,
    skips: [],
    highestSeverity: '',
  };

  for (const ev of events) {
    switch (ev.type) {
      case 'run.start':
        summary.runId = ev.runStart.runId;
        summary.root = ev.runStart.scope.root;
        break;
      case 'detect.finding':
        summary.findings.push(ev.detectFinding.finding);
        break;
      case 'ant.skipped':
        summary.skips.push({
          reason: ev.antSkipped.reason,
          check: ev.antSkipped.failedCheck.name,
          finding: ev.antSkipped.finding,
        });
        break;
      case 'run.end':
        summary.verifiedCount = ev.runEnd.verified;
        summary.skippedCount = ev.runEnd.skipped;
        summary.appliedCount = ev.runEnd.applied;
        summary.highestSeverity = ev.runEnd.highestSeverity;
        if (ev.runEnd.error) summary.error = ev.runEnd.error;
        break;
      // ant.start / ant.verified / apply.done carry no summary-level rollup
      // beyond the run.end aggregate; the front doors render them live via
      // onEvent if they want per-ant detail.
      default:
        break;
    }
  }

  return summary;
}
