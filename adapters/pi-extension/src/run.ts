/**
 * Pi extension entrypoint. A thin shell: parse the verb + args, exec
 * `ant <verb> --json` via the SHARED runAnt seam, and print a markdown summary.
 * No engine logic — the parsing/contract lives in adapters/shared, the engine
 * lives in the Go binary (TECHSPEC §3).
 *
 * Usage: node run.ts [scout|fix|review|apply] [path] [extra ant flags...]
 */

import { runAnt, summarize } from '../../shared/src/index.ts';
import { renderMarkdown } from './render.ts';

const VERBS = new Set(['scout', 'fix', 'review', 'apply']);

async function main(): Promise<number> {
  const argv = process.argv.slice(2).filter((a) => a !== '');
  const verb = argv.length > 0 && VERBS.has(argv[0]!) ? argv[0]! : 'scout';
  const rest = argv.length > 0 && VERBS.has(argv[0]!) ? argv.slice(1) : argv;

  try {
    const { events, exitCode } = await runAnt(verb, rest, {
      bin: process.env.ANT_BIN ?? 'ant',
    });
    process.stdout.write(renderMarkdown(summarize(events)) + '\n');
    return exitCode;
  } catch (err) {
    process.stderr.write(
      `ant-colony extension error: ${err instanceof Error ? err.message : String(err)}\n`,
    );
    return 2;
  }
}

main().then((code) => {
  process.exitCode = code;
});
