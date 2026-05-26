package trailingdebug

import "fmt"

// Greet returns a greeting. The fmt.Println below is ad-hoc debug output left in
// the source; trailing-debug-code flags it (propose-only). The Sprintf use of
// fmt keeps the import live, so removing the debug line still compiles — proving
// the fix is safe rather than merely passing because the import vanished.
func Greet(name string) string {
	fmt.Println("DEBUG: Greet called with", name)
	return fmt.Sprintf("hello, %s", name)
}
