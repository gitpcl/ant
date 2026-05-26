// Package stalepin is a hermetic fixture for the stale-dependency-pin species:
// its go.mod lists `rsc.io/quote v1.5.2` TWICE in the require block (a duplicate
// pin from a stray edit). The detector flags the second occurrence; the
// deterministic delete-match fix removes that redundant require line; the
// command: verifier proves `go build`/`go vet` still pass. The code imports only
// the standard library, so the module builds entirely offline — the leftover
// (still-unused) pin is harmless to an offline build because nothing imports it.
package stalepin

import "strings"

// Upper uppercases s using only the standard library.
func Upper(s string) string {
	return strings.ToUpper(s)
}
