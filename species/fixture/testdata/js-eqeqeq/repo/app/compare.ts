// compare.ts holds exactly ONE js-eqeqeq finding (the loose `==` below).
// detector-clears matches on species+file, so a fixture file carries a single
// finding of the species. The `== null` idiom in guard.ts is intentionally NOT a
// finding (it is a deliberate null/undefined check the rule preserves).
export function sameId(a: number, b: number): boolean {
	return a == b;
}
