/**
 * Recorded-stream parse test for the Claude Code skill front-door. It feeds the
 * shared recorded `--json` fixtures (derived from the frozen contract in
 * internal/engine/events/event.go + the scout golden) through the shared parser
 * and the skill's renderer, asserting the thin shell turns a run.start..run.end
 * stream into the expected typed events and summary. No live `ant` binary.
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { parseStream, summarize } from '../../shared/src/index.ts';
import { renderSummary } from '../src/render.ts';

const here = dirname(fileURLToPath(import.meta.url));
const sharedFixtures = join(here, '..', '..', 'shared', 'test', 'fixtures');
const fixture = (name: string): string =>
  readFileSync(join(sharedFixtures, name), 'utf8');

test('skill parses the recorded scout stream run.start..run.end', () => {
  const events = parseStream(fixture('scout.ndjson'));
  assert.equal(events[0]?.type, 'run.start');
  assert.equal(events[events.length - 1]?.type, 'run.end');
  assert.equal(events.filter((e) => e.type === 'detect.finding').length, 2);
});

test('skill renders a scout summary with findings (no engine logic, just the stream)', () => {
  const out = renderSummary(summarize(parseStream(fixture('scout.ndjson'))));
  assert.match(out, /2 finding\(s\)/);
  assert.match(out, /highest severity: high/);
  assert.match(out, /unused-import main\.go:3/);
});

test('skill renders a fix summary that surfaces the verifier SKIP and its reason', () => {
  const out = renderSummary(summarize(parseStream(fixture('fix-run.ndjson'))));
  assert.match(out, /1 verified/);
  assert.match(out, /1 skipped/);
  assert.match(out, /1 applied/);
  // A skip is a trust signal — the failing check and reason must be visible.
  assert.match(out, /failed "tests:affected"/);
  assert.match(out, /Skipped \(verification gate/);
});

test('skill reports an operational error from run.end.error and stops', () => {
  const errored = [
    '{"type":"run.start","seq":1,"time":"t","runStart":{"runId":"r","scope":{"root":"."}}}',
    '{"type":"run.end","seq":2,"time":"t","runEnd":{"runId":"r","findings":0,"verified":0,"skipped":0,"applied":0,"highestSeverity":"","error":"ast-grep binary not found"}}',
  ].join('\n');
  const out = renderSummary(summarize(parseStream(errored)));
  assert.match(out, /Ant run FAILED: ast-grep binary not found/);
});
