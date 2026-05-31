/**
 * Recorded-fixture parse tests for the shared `--json` parser. The fixtures are
 * captured/derived from the FROZEN contract: scout.ndjson is byte-identical to
 * the Go golden (internal/engine/scout/testdata/scout-json.golden) and
 * fix-run.ndjson exercises every event type with the exact nested shapes from
 * internal/engine/events/event.go. If the engine contract changes, these tests
 * break — which is the point: the front doors must stay in lockstep.
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import {
  parseStream,
  parseLine,
  summarize,
  StreamSplitter,
  StreamError,
  EVENT_TYPES,
} from '../src/index.ts';
import type { AntEvent, EventType } from '../src/index.ts';

const here = dirname(fileURLToPath(import.meta.url));
const fixture = (name: string): string =>
  readFileSync(join(here, 'fixtures', name), 'utf8');

/** ofType asserts an event of the given discriminant exists and returns it narrowed. */
function ofType<T extends EventType>(events: AntEvent[], type: T): Extract<AntEvent, { type: T }> {
  const ev = events.find((e) => e.type === type);
  assert.ok(ev, `expected a ${type} event`);
  return ev as Extract<AntEvent, { type: T }>;
}

test('scout fixture (the Go golden) parses into the expected typed events', () => {
  const events = parseStream(fixture('scout.ndjson'));
  assert.deepEqual(
    events.map((e) => e.type),
    ['run.start', 'detect.finding', 'detect.finding', 'run.end'],
  );

  const start = ofType(events, 'run.start');
  assert.equal(start.runStart.runId, 'golden-run');
  assert.equal(start.runStart.scope.root, 'testdata/has-findings');
  // The --json contract carries a machine-readable schema version on run.start
  // (events.SchemaVersion). The front doors read it at stream open to detect a
  // breaking change; pin the baseline here in lockstep with the Go golden.
  assert.equal(start.runStart.schemaVersion, '1');

  const first = ofType(events, 'detect.finding');
  const f = first.detectFinding.finding;
  assert.equal(f.species, 'unused-import');
  assert.equal(f.severity, 'high');
  assert.equal(f.span.startLine, 3);
  assert.equal(f.meta?.ruleId, 'unused-import');

  const end = ofType(events, 'run.end');
  assert.equal(end.runEnd.findings, 2);
  assert.equal(end.runEnd.highestSeverity, 'high');
});

test('fix-run fixture parses all seven event types in order', () => {
  const events = parseStream(fixture('fix-run.ndjson'));
  const seen = new Set(events.map((e) => e.type));
  for (const t of EVENT_TYPES) {
    assert.ok(seen.has(t), `expected the stream to contain a ${t} event`);
  }
});

test('ant.verified carries the diff + provenance + passing checks', () => {
  const events = parseStream(fixture('fix-run.ndjson'));
  const verified = ofType(events, 'ant.verified');
  assert.equal(verified.antVerified.diff.fixer, 'deterministic (delete-match)');
  assert.equal(verified.antVerified.diff.files[0]?.path, 'main.go');
  assert.equal(verified.antVerified.verify.passed, true);
  const checks = verified.antVerified.verify.checks.map((c) => c.name);
  assert.deepEqual(checks, ['diff-bounded', 'compile', 'detector-clears']);
});

test('ant.skipped surfaces the failing check + reason (a skip is a trust signal)', () => {
  const events = parseStream(fixture('fix-run.ndjson'));
  const skipped = ofType(events, 'ant.skipped');
  assert.equal(skipped.antSkipped.failedCheck.name, 'tests:affected');
  assert.equal(skipped.antSkipped.failedCheck.passed, false);
  assert.match(skipped.antSkipped.reason, /tests:affected failed/);
  assert.equal(skipped.antSkipped.verify.passed, false);
});

test('apply.done carries branch + commit provenance', () => {
  const events = parseStream(fixture('fix-run.ndjson'));
  const applied = ofType(events, 'apply.done');
  assert.equal(applied.applyDone.branch, 'ant/fix-golden');
  assert.equal(applied.applyDone.commit?.length, 40);
});

test('summarize folds the stream into a render-ready summary', () => {
  const summary = summarize(parseStream(fixture('fix-run.ndjson')));
  assert.equal(summary.runId, 'fix-golden');
  assert.equal(summary.findings.length, 2);
  assert.equal(summary.verifiedCount, 1);
  assert.equal(summary.skippedCount, 1);
  assert.equal(summary.appliedCount, 1);
  assert.equal(summary.skips.length, 1);
  assert.equal(summary.skips[0]?.check, 'tests:affected');
  assert.equal(summary.highestSeverity, 'high');
  assert.equal(summary.error, undefined);
});

test('StreamSplitter reassembles events split across chunk boundaries', () => {
  const text = fixture('scout.ndjson');
  const splitter = new StreamSplitter();
  const out = [];
  // Feed the stream one byte at a time — the worst-case chunking.
  for (const ch of text) out.push(...splitter.push(ch));
  out.push(...splitter.flush());
  assert.deepEqual(
    out.map((e) => e.type),
    ['run.start', 'detect.finding', 'detect.finding', 'run.end'],
  );
});

test('parseLine rejects an unknown event type (fails loudly, never guesses)', () => {
  assert.throws(
    () => parseLine('{"type":"ant.exploded","seq":1,"time":"t"}'),
    StreamError,
  );
});

test('parseStream rejects a stream that does not end with run.end', () => {
  const truncated = fixture('scout.ndjson').trim().split('\n').slice(0, 2).join('\n');
  assert.throws(() => parseStream(truncated), StreamError);
});

test('parseStream rejects non-increasing seq (drop/reorder detection)', () => {
  const lines = [
    '{"type":"run.start","seq":2,"time":"t","runStart":{"runId":"r","scope":{"root":"."}}}',
    '{"type":"run.end","seq":1,"time":"t","runEnd":{"runId":"r","findings":0,"verified":0,"skipped":0,"applied":0,"highestSeverity":""}}',
  ].join('\n');
  assert.throws(() => parseStream(lines), StreamError);
});
