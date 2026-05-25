/**
 * Shared front-door core for Ant. The two adapters (Claude Code skill, Pi
 * extension) import from here so the `--json` contract is parsed in exactly one
 * place and they cannot drift apart or away from the Go engine (TECHSPEC §3/§12).
 */

export * from './events.ts';
export * from './parser.ts';
export * from './exec.ts';
export * from './summary.ts';
