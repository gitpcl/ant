package uncheckedassertion

import "fmt"

// AsString is the unchecked-type-assertion smell: it asserts the dynamic type of
// v to string with the SINGLE-result form `s := v.(string)`. If v is not a
// string the assertion PANICS at runtime instead of reporting the mismatch. The
// unchecked-type-assertion species nominates the `s := v.(string)` form, the
// recorded fix switches to the comma-ok form and returns an error on the not-ok
// branch, and the verifier gate (compile + tests:affected + detector-clears)
// confirms it.
func AsString(v interface{}) string {
	s := v.(string)
	return s
}

// describe is a benign helper so the package has a second function; it does not
// contain the smell (no unchecked assertion).
func describe(v interface{}) string {
	return fmt.Sprintf("%T", v)
}
