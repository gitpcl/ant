// count.ts is the NON-MATCHING discrimination case: every annotation is a
// precise type, so ts-no-explicit-any must report no finding here.
export function size(items: readonly string[]): number {
	return items.length;
}
