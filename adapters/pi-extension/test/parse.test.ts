/**
 * Recorded-stream parse test for the Pi extension front-door. Same contract,
 * same SHARED fixtures and parser as the Claude Code skill — only the
 * presentation differs. Asserts the thin shell parses run.start..run.end and
 * renders the run (including the verifier skip + reason). No live `ant` binary.
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { parseStream, summarize } from '../../shared/src/index.ts';
import { renderMarkdown } from '../src/render.ts';

const here = dirname(fileURLToPath(import.meta.url));
const sharedFixtures = join(here, '..', '..', 'shared', 'test', 'fixtures');
const fixture = (name: string): string =>
  readFileSync(join(sharedFixtures, name), 'utf8');

test('extension parses the recorded scout stream run.start..run.end', () => {
  const events = parseStream(fixture('scout.ndjson'));
  assert.equal(events[0]?.type, 'run.start');
  assert.equal(events[events.length - 1]?.type, 'run.end');
  assert.equal(events.filter((e) => e.type === 'detect.finding').length, 2);
});

test('extension renders a scout summary as markdown (pure formatting, no logic)', () => {
  const md = renderMarkdown(summarize(parseStream(fixture('scout.ndjson'))));
  assert.match(md, /### Ant run `golden-run`/);
  assert.match(md, /Findings: \*\*2\*\*/);
  assert.match(md, /Highest severity: \*\*high\*\*/);
});

test('extension surfaces the verifier SKIP and reason from a fix run', () => {
  const md = renderMarkdown(summarize(parseStream(fixture('fix-run.ndjson'))));
  assert.match(md, /Verified: \*\*1\*\*/);
  assert.match(md, /Skipped: \*\*1\*\*/);
  assert.match(md, /Applied: \*\*1\*\*/);
  assert.match(md, /failed `tests:affected`/);
});

test('extension reports an operational error from run.end.error', () => {
  const errored = [
    '{"type":"run.start","seq":1,"time":"t","runStart":{"runId":"r","scope":{"root":"."}}}',
    '{"type":"run.end","seq":2,"time":"t","runEnd":{"runId":"r","findings":0,"verified":0,"skipped":0,"applied":0,"highestSeverity":"","error":"ast-grep binary not found"}}',
  ].join('\n');
  const md = renderMarkdown(summarize(parseStream(errored)));
  assert.match(md, /Ant run failed:\*\* ast-grep binary not found/);
});
