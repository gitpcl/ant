/**
 * Pi extension registration surface. A Pi host imports `activate` and calls the
 * exported command handler. This stays a THIN shell: it wires the shared exec +
 * parse + render together and returns the rendered result + the engine's exit
 * code. It owns no detection/fix/trust logic (TECHSPEC §3).
 */

import { runAnt, summarize } from '../../shared/src/index.ts';
import type { AntEvent } from '../../shared/src/index.ts';
import { renderMarkdown } from './render.ts';

const VERBS = new Set(['scout', 'fix', 'review', 'apply']);

export interface AntCommandResult {
  /** Rendered markdown for Pi to display. */
  markdown: string;
  /** The engine's exit code: 0 ok, 1 findings >= --fail-on, 2 operational. */
  exitCode: number;
  /** The raw typed events, in case the host wants to render them itself. */
  events: AntEvent[];
}

/**
 * runAntCommand is the extension's single command handler. `args` is the verb
 * plus any path/flags the user passed; it defaults to `scout`.
 */
export async function runAntCommand(
  args: string[],
  opts: { bin?: string; cwd?: string } = {},
): Promise<AntCommandResult> {
  const cleaned = args.filter((a) => a !== '');
  const verb = cleaned.length > 0 && VERBS.has(cleaned[0]!) ? cleaned[0]! : 'scout';
  const rest = cleaned.length > 0 && VERBS.has(cleaned[0]!) ? cleaned.slice(1) : cleaned;

  const { events, exitCode } = await runAnt(verb, rest, {
    bin: opts.bin ?? process.env.ANT_BIN ?? 'ant',
    cwd: opts.cwd,
  });

  return { markdown: renderMarkdown(summarize(events)), exitCode, events };
}

/** Pi host activation hook: register the `ant` command. */
export function activate(register: (name: string, handler: typeof runAntCommand) => void): void {
  register('ant', runAntCommand);
}
