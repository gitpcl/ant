// view.js — Sprint 025 js-multilang-backfill .js coverage for unused-import. The
// marked import below is unused; unused-import's `language: javascript` doc (marker-
// gated, since JS has no compiler to prove it like Go) removes it. This is a JS-ONLY
// repo so the shared species' `compile` gate is a vacuous Go-build pass — the proof
// is the marker + detector-clears, the same gradient the TS species use.
//ant:unused-import no longer used after the rewrite
import legacy from "./legacy-helpers.js";

export function render() {
	return "<div></div>";
}
