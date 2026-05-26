package missingcontexttimeout

import "context"

// query models a blocking network/DB call: it takes a context and a key and
// returns a value. A real implementation would honor ctx's deadline/cancel.
func query(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "value-for-" + key, nil
}

// Fetch is the missing-context-timeout smell: it calls query with
// `context.Background()` passed DIRECTLY, so the call has no deadline and can
// hang forever on a slow dependency. The missing-context-timeout species
// nominates the inline `context.Background()` argument, the recorded fix derives
// a context.WithTimeout (with defer cancel) and passes that instead, and the
// verifier gate (compile + tests:affected + detector-clears) confirms it.
func Fetch(key string) (string, error) {
	return query(context.Background(), key)
}
