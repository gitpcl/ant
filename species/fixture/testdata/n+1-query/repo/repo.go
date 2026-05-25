package nplusone

// User is the row Names collects display strings from.
type User struct{ Name string }

// lookupUser models a single-row query: one call == one round trip. Calling it
// once per loop iteration is the N+1 pattern this species targets.
func lookupUser(id int) User {
	return User{Name: names[id]}
}

// lookupUsers is the batched form: one call fetches every row at once. The
// recorded fix hoists the per-iteration lookupUser call up to a single
// lookupUsers call before the loop.
func lookupUsers(ids []int) []User {
	out := make([]User, 0, len(ids))
	for _, id := range ids {
		out = append(out, lookupUser(id))
	}
	return out
}

// names is the fixture's tiny in-memory backing store, keyed by id.
var names = map[int]string{1: "ada", 2: "linus", 3: "grace"}

// Names is the N+1-query smell: it issues one lookupUser query PER iteration of
// the loop. The n+1-query species nominates the per-iteration call inside the
// range loop; the recorded fix batches it into a single lookupUsers call before
// the loop, and the verifier gate (compile + tests:affected + detector-clears)
// confirms the behavior is preserved and the loop no longer queries per item.
func Names(ids []int) []string {
	var names []string
	for _, id := range ids {
		u := lookupUser(id)
		names = append(names, u.Name)
	}
	return names
}
