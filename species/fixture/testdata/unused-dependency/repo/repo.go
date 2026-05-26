// Package unuseddep is a hermetic fixture for the unused-dependency species: its
// go.mod declares `require rsc.io/quote v1.5.2`, but NO source file imports it —
// the only import is the standard library `fmt`. The detector cross-references
// the declared require against the used imports and flags rsc.io/quote; the
// deterministic delete-match fix removes the require line; the command: verifier
// proves `go build`/`go vet` still pass with NO external dependency (offline).
package unuseddep

import "fmt"

// Greeting returns a fixed greeting. It deliberately uses only the standard
// library, so once the unused rsc.io/quote require is removed the module builds
// and vets entirely offline (the hermetic-fixture requirement).
func Greeting(name string) string {
	return fmt.Sprintf("hello, %s", name)
}
