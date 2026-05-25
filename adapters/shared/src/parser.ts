/**
 * The shared `--json` stream parser used by BOTH front doors (DRY: the
 * event-stream parsing lives here once). Ant's `--json` renderer emits one JSON
 * object per line (newline-delimited JSON); this turns that byte/text stream
 * into typed AntEvent records.
 *
 * No engine logic — this only decodes. It validates the structural contract
 * (known event type, populated payload, monotonic seq) so a malformed stream
 * fails loudly at the boundary instead of silently mis-rendering (input
 * validation at the system boundary, per the coding-style rules).
 */

import type { AntEvent, EventType } from './events.ts';
import { isEventType } from './events.ts';

/** Maps each event type to its payload property name on the event object. */
const PAYLOAD_KEY: Record<EventType, string> = {
  'run.start': 'runStart',
  'detect.finding': 'detectFinding',
  'ant.start': 'antStart',
  'ant.verified': 'antVerified',
  'ant.skipped': 'antSkipped',
  'apply.done': 'applyDone',
  'run.end': 'runEnd',
};

/** Thrown when a line is not a well-formed Ant event. */
export class StreamError extends Error {}

/**
 * parseLine decodes a single NDJSON line into a typed AntEvent. Blank lines
 * return null (a tolerated stream artifact). A line that parses as JSON but is
 * not a valid event throws StreamError — we never guess.
 */
export function parseLine(line: string): AntEvent | null {
  const trimmed = line.trim();
  if (trimmed === '') return null;

  let obj: unknown;
  try {
    obj = JSON.parse(trimmed);
  } catch (e) {
    throw new StreamError(`invalid JSON in event line: ${(e as Error).message}`);
  }

  if (typeof obj !== 'object' || obj === null) {
    throw new StreamError('event line is not a JSON object');
  }

  const rec = obj as Record<string, unknown>;
  const type = rec.type;
  if (typeof type !== 'string' || !isEventType(type)) {
    throw new StreamError(`unknown or missing event type: ${JSON.stringify(type)}`);
  }
  if (typeof rec.seq !== 'number') {
    throw new StreamError(`event ${type} missing numeric seq`);
  }
  const payloadKey = PAYLOAD_KEY[type];
  if (rec[payloadKey] === undefined) {
    throw new StreamError(`event ${type} missing payload field "${payloadKey}"`);
  }

  // The structural checks above guarantee the discriminated-union shape.
  return rec as unknown as AntEvent;
}

/**
 * parseStream decodes a whole `--json` capture (a string of NDJSON) into an
 * ordered array of typed events, asserting the stream is well-formed: it begins
 * with run.start, ends with run.end, and seq is strictly increasing. These are
 * contract invariants the Go renderer guarantees (TECHSPEC §12), so a violation
 * means the binary or the capture is wrong — fail loudly.
 */
export function parseStream(text: string): AntEvent[] {
  const events: AntEvent[] = [];
  let lastSeq = 0;
  for (const line of text.split('\n')) {
    const ev = parseLine(line);
    if (ev === null) continue;
    if (ev.seq <= lastSeq) {
      throw new StreamError(`non-increasing seq: ${ev.seq} after ${lastSeq}`);
    }
    lastSeq = ev.seq;
    events.push(ev);
  }

  const first = events[0];
  const last = events[events.length - 1];
  if (first === undefined || last === undefined) {
    throw new StreamError('empty event stream');
  }
  if (first.type !== 'run.start') {
    throw new StreamError(`stream must start with run.start, got ${first.type}`);
  }
  if (last.type !== 'run.end') {
    throw new StreamError(`stream must end with run.end, got ${last.type}`);
  }
  return events;
}

/**
 * StreamSplitter incrementally turns chunks of stdout (which may split a line
 * across chunk boundaries) into complete events as they arrive. Front doors use
 * this to render progress live while `ant` is still running. Pure text->events;
 * the subprocess wiring lives in exec.ts.
 */
export class StreamSplitter {
  private buffer = '';

  /** push feeds a chunk and returns any complete events it completed. */
  push(chunk: string): AntEvent[] {
    this.buffer += chunk;
    const out: AntEvent[] = [];
    let nl: number;
    while ((nl = this.buffer.indexOf('\n')) !== -1) {
      const line = this.buffer.slice(0, nl);
      this.buffer = this.buffer.slice(nl + 1);
      const ev = parseLine(line);
      if (ev !== null) out.push(ev);
    }
    return out;
  }

  /** flush parses any trailing line not terminated by a newline. */
  flush(): AntEvent[] {
    const rest = this.buffer;
    this.buffer = '';
    const ev = parseLine(rest);
    return ev === null ? [] : [ev];
  }
}
