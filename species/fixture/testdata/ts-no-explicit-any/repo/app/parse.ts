// parse.ts holds exactly ONE ts-no-explicit-any finding (the `: any` annotation
// on `total` below). detector-clears matches on species+file, so a fixture file
// carries a single `any`. count.ts is the non-matching case (no `any`).
export function sumPrices(items: { price: number }[]): number {
	let total: any = 0;
	for (const item of items) {
		total += item.price;
	}
	return total;
}
