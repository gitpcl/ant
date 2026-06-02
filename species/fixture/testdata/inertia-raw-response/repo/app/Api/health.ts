// health.ts is a genuine JSON API handler OUTSIDE a Controllers/ path — the
// `files` glob confines inertia-raw-response to Inertia controllers, so this
// raw JSON return must NOT be flagged.
interface Res { json(body: unknown): Res }
export function health(_req: unknown, res: Res): Res {
	return res.json({ ok: true });
}
