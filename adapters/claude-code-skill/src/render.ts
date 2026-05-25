/**
 * Render a parsed Ant run as a plain-text summary for the Claude Code skill.
 * Pure formatting over the shared RunSummary — no engine logic. Kept separate
 * from run.ts so it is unit-testable without spawning a subprocess.
 */

import type { RunSummary, AntEvent } from '../../shared/src/index.ts';

/** renderSummary turns a RunSummary into the text the skill prints to stdout. */
export function renderSummary(summary: RunSummary): string {
  const lines: string[] = [];

  if (summary.error) {
    lines.push(`Ant run FAILED: ${summary.error}`);
    return lines.join('\n');
  }

  lines.push(
    `Ant run ${summary.runId} on ${summary.root}: ` +
      `${summary.findings.length} finding(s), ` +
      `${summary.verifiedCount} verified, ` +
      `${summary.skippedCount} skipped, ` +
      `${summary.appliedCount} applied ` +
      `(highest severity: ${summary.highestSeverity || 'none'}).`,
  );

  if (summary.findings.length > 0) {
    lines.push('', 'Findings:');
    for (const f of summary.findings) {
      lines.push(
        `  - [${f.severity}] ${f.species} ${f.file}:${f.span.startLine} — ${f.message}`,
      );
    }
  }

  if (summary.skips.length > 0) {
    lines.push('', 'Skipped (verification gate — surfaced, not hidden):');
    for (const s of summary.skips) {
      lines.push(
        `  - ${s.finding.species} ${s.finding.file}:${s.finding.span.startLine} ` +
          `failed "${s.check}": ${s.reason}`,
      );
    }
  }

  return lines.join('\n');
}

/** renderEventLine formats a single event for live, per-event progress output. */
export function renderEventLine(ev: AntEvent): string {
  switch (ev.type) {
    case 'run.start':
      return `> run ${ev.runStart.runId} started on ${ev.runStart.scope.root}`;
    case 'detect.finding': {
      const f = ev.detectFinding.finding;
      return `  found [${f.severity}] ${f.species} ${f.file}:${f.span.startLine}`;
    }
    case 'ant.start':
      return `  ant ${ev.antStart.antId} picking up ${ev.antStart.finding.species}`;
    case 'ant.verified':
      return `  ant ${ev.antVerified.antId} verified (${ev.antVerified.diff.fixer})`;
    case 'ant.skipped':
      return `  ant ${ev.antSkipped.antId} SKIPPED: ${ev.antSkipped.failedCheck.name} — ${ev.antSkipped.reason}`;
    case 'apply.done':
      return `  applied ${ev.applyDone.path}${ev.applyDone.branch ? ` on ${ev.applyDone.branch}` : ''}`;
    case 'run.end':
      return `> run ${ev.runEnd.runId} ended (highest: ${ev.runEnd.highestSeverity || 'none'})`;
  }
}
