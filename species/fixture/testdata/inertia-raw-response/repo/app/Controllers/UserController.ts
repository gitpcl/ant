// UserController.ts is an Inertia controller (path under Controllers/). It holds
// exactly ONE inertia-raw-response finding: the `return res.json(...)` in show()
// bypasses Inertia. index() correctly returns Inertia.render(...) and is the
// non-matching discrimination case. detector-clears matches on species+file, so
// the fixture carries a single raw-JSON return.
import { Inertia } from "./inertia";

interface Req {
	params: { id: string };
}
interface Res {
	json(body: unknown): Res;
}

export function show(req: Req, res: Res) {
	const user = { id: req.params.id, name: "Ada" };
	return res.json({ user });
}

export function index(_req: Req, _res: Res) {
	return Inertia.render("Users/Index", { users: [] });
}
