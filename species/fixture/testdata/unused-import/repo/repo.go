package unusedimport

import "strings"

// Greet returns a friendly greeting. The "strings" import above is never
// referenced anywhere in this file, so it is an unused import: Go refuses to
// compile this file as-is, and the unused-import species removes the import to
// make it build. The detect→fix→verify harness asserts exactly that.
func Greet(name string) string {
	return "hello, " + name
}
