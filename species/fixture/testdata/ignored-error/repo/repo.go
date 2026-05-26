package ignorederror

import "strconv"

// parsePort models a fallible parse: it returns an (int, error) pair, so a
// caller that discards the error with `_` can proceed with a zero/invalid port
// after a parse failure.
func parsePort(raw string) (int, error) {
	return strconv.Atoi(raw)
}

// Port is the ignored-error smell: it discards parsePort's error with `_` and
// returns the (possibly-zero, never-validated) port. On a bad input the failure
// is silently swallowed and the caller gets 0 with no indication anything went
// wrong. The ignored-error species nominates the `v, _ := call()` discard, the
// recorded fix binds and propagates err, and the verifier gate (compile +
// tests:affected + detector-clears) confirms it.
func Port(raw string) int {
	port, _ := parsePort(raw)
	return port
}
