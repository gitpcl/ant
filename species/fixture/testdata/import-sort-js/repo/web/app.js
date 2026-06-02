// app.js — Sprint 025 js-multilang-backfill detect-only proof for import-sort's
// `language: javascript` doc. The marked, out-of-order JS imports are NOMINATED by
// the detector (the external organizer does the sort); this case proves the doc
// fires on .js. Wired through RunDetectOnlyCase (detect + no-mutation assertion).
//ant:import-sort imports below are out of canonical order
import zebra from "./zebra.js";
import apple from "./apple.js";

export function boot() {
	return [apple, zebra];
}
