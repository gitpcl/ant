/**
 * Render a parsed Ant run for the Pi extension. Like the Claude skill's
 * renderer, this is pure formatting over the SHARED RunSummary (the contract
 * parsing is shared in adapters/shared; only the host-idiomatic presentation
 * differs). No engine logic.
 */

import type { RunSummary } from '../../shared/src/index.ts';

/** renderMarkdown turns a RunSummary into a Pi-friendly markdown block. */
export function renderMarkdown(summary: RunSummary): string {
  if (summary.error) {
    return `**Ant run failed:** ${summary.error}`;
  }

  const out: string[] = [];
  out.push(`### Ant run \`${summary.runId}\` — \`${summary.root}\``);
  out.push('');
  out.push(`- Findings: **${summary.findings.length}**`);
  out.push(`- Verified: **${summary.verifiedCount}**`);
  out.push(`- Skipped: **${summary.skippedCount}**`);
  out.push(`- Applied: **${summary.appliedCount}**`);
  out.push(`- Highest severity: **${summary.highestSeverity || 'none'}**`);

  if (summary.findings.length > 0) {
    out.push('', '**Findings**');
    for (const f of summary.findings) {
      out.push(`- \`${f.severity}\` ${f.species} — ${f.file}:${f.span.startLine} — ${f.message}`);
    }
  }

  if (summary.skips.length > 0) {
    out.push('', '**Skipped (verification gate — surfaced, not hidden)**');
    for (const s of summary.skips) {
      out.push(
        `- ${s.finding.species} ${s.finding.file}:${s.finding.span.startLine} — ` +
          `failed \`${s.check}\`: ${s.reason}`,
      );
    }
  }

  return out.join('\n');
}
