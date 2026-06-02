// sum.js — Sprint 025 js-multilang-backfill detect-only proof for ai-slop's
// `language: javascript` doc. The redundant `const result` declared and immediately
// returned is the AI-slop tic; this case proves the doc fires on .js. ai-slop ships
// DISABLED by default and is fuzzy/propose-only, so a detect-only proof (no golden)
// is the right scope. Wired through RunDetectOnlyCase.
export function sum(a, b) {
	const result = a + b;
	return result;
}
