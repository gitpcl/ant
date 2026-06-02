// guard.ts is the NON-MATCHING discrimination case: `value == null` is the
// deliberate null-or-undefined idiom; js-eqeqeq must NOT rewrite it to `===`
// (that would change behavior). The right-hand-side `not null/undefined`
// constraint preserves it.
export function isMissing(value: unknown): boolean {
	return value == null;
}
