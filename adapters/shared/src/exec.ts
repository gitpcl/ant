/**
 * The thin subprocess seam: run `ant <verb> --json` and stream its events. This
 * is the ONLY thing the front doors do besides rendering — they own no engine
 * logic (TECHSPEC §3). Built on node:child_process + the shared StreamSplitter;
 * ZERO runtime dependencies.
 */

import { spawn } from 'node:child_process';
import type { AntEvent } from './events.ts';
import { StreamSplitter, StreamError } from './parser.ts';

export interface RunOptions {
  /** Path to the ant binary; defaults to "ant" on PATH. */
  bin?: string;
  /** Working directory for the run. */
  cwd?: string;
  /** Called for each event as it arrives (live rendering). */
  onEvent?: (ev: AntEvent) => void;
  /** Environment overrides merged onto process.env. */
  env?: NodeJS.ProcessEnv;
}

export interface RunResult {
  /** Every event the run emitted, in order. */
  events: AntEvent[];
  /** The process exit code: 0 ok, 1 findings >= --fail-on, 2 operational. */
  exitCode: number;
}

/**
 * runAnt execs `ant <verb> [...args] --json`, decodes the NDJSON event stream,
 * and resolves with the collected events and exit code. `--json` is appended if
 * not already present so callers cannot accidentally request human output.
 *
 * It does NOT throw on a non-zero exit code — exit 1 (findings) and exit 2
 * (operational error, surfaced as run.end.error) are normal outcomes the front
 * door renders. It rejects only on a spawn failure (e.g. binary not found) or a
 * malformed stream (StreamError).
 */
export function runAnt(
  verb: string,
  args: string[] = [],
  opts: RunOptions = {},
): Promise<RunResult> {
  const bin = opts.bin ?? 'ant';
  const argv = [verb, ...args];
  if (!argv.includes('--json')) argv.push('--json');

  return new Promise((resolve, reject) => {
    const child = spawn(bin, argv, {
      cwd: opts.cwd,
      env: opts.env ? { ...process.env, ...opts.env } : process.env,
      stdio: ['ignore', 'pipe', 'pipe'],
    });

    const splitter = new StreamSplitter();
    const events: AntEvent[] = [];
    let stderr = '';
    let streamErr: Error | null = null;

    const consume = (evs: AntEvent[]) => {
      for (const ev of evs) {
        events.push(ev);
        opts.onEvent?.(ev);
      }
    };

    child.stdout.setEncoding('utf8');
    child.stdout.on('data', (chunk: string) => {
      if (streamErr) return;
      try {
        consume(splitter.push(chunk));
      } catch (e) {
        streamErr = e as Error;
      }
    });

    child.stderr.setEncoding('utf8');
    child.stderr.on('data', (chunk: string) => {
      stderr += chunk;
    });

    child.on('error', (err) => {
      reject(new Error(`failed to exec ${bin}: ${err.message}`));
    });

    child.on('close', (code) => {
      if (!streamErr) {
        try {
          consume(splitter.flush());
        } catch (e) {
          streamErr = e as Error;
        }
      }
      if (streamErr) {
        reject(
          new StreamError(
            `${(streamErr as Error).message}${stderr ? `\nstderr: ${stderr.trim()}` : ''}`,
          ),
        );
        return;
      }
      resolve({ events, exitCode: code ?? 0 });
    });
  });
}
