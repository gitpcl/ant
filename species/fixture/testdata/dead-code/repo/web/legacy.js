// web/legacy.js — Sprint 025 js-multilang-backfill .js coverage. The function
// below is marked dead; dead-code now removes it in plain JavaScript via its
// `language: javascript` doc. One finding per file (detector-clears matches
// species+file).
//ant:dead-code superseded by the new pipeline
function legacyTransform(rows) {
	return rows.map((r) => r.id);
}

export const ACTIVE = true;
