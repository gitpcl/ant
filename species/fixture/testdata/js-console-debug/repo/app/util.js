// util.js holds exactly ONE js-console-debug finding in plain JavaScript (the
// console.log below) — the .js coverage the Sprint 025 wave adds via the
// `language: javascript` detector doc. allowJs lets the tsc --noEmit gate cover
// it without a type annotation.
export function total(items) {
	console.log("DEBUG: total over", items.length);
	return items.reduce((a, b) => a + b, 0);
}
