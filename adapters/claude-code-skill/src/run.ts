/**
 * Claude Code skill entrypoint. A thin shell: parse the verb + args, exec
 * `ant <verb> --json` via the shared runAnt seam, and print a summary. No engine
 * logic — see SKILL.md and TECHSPEC §3.
 *
 * Usage: node run.ts [scout|fix|review|apply] [path] [extra ant flags...]
 * Defaults to `scout` when no verb is given (matches the bare-`ant` affordance).
 */

import { runAnt, summarize } from '../../shared/src/index.ts';
import { renderSummary, renderEventLine } from './render.ts';

const VERBS = new Set(['scout', 'fix', 'review', 'apply']);

async function main(): Promise<number> {
  const argv = process.argv.slice(2).filter((a) => a !== '');
  const verb = argv.length > 0 && VERBS.has(argv[0]!) ? argv[0]! : 'scout';
  const rest = argv.length > 0 && VERBS.has(argv[0]!) ? argv.slice(1) : argv;

  try {
    const { events, exitCode } = await runAnt(verb, rest, {
      bin: process.env.ANT_BIN ?? 'ant',
      onEvent: (ev) => process.stderr.write(renderEventLine(ev) + '\n'),
    });
    process.stdout.write(renderSummary(summarize(events)) + '\n');
    // Relay the engine's exit code (0/1/2) so CI usage of the skill is honest.
    return exitCode;
  } catch (err) {
    process.stderr.write(
      `ant-colony skill error: ${err instanceof Error ? err.message : String(err)}\n`,
    );
    return 2;
  }
}

main().then((code) => {
  process.exitCode = code;
});
