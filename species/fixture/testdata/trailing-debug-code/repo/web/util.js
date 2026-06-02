// web/util.js — Sprint 025 js-multilang-backfill .js coverage case. The
// console.log below is the JavaScript debug-output tic trailing-debug-code now
// flags via its `language: javascript` doc. One finding per file (detector-clears
// matches species+file).
export function total(items) {
	console.log("DEBUG: total", items.length);
	return items.reduce((a, b) => a + b, 0);
}
