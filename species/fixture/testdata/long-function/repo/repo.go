package longfunc

// Process computes a combined product over four inputs. It is written as one
// 7-statement function (x, y, z, p, q, r, return) — past the long-function
// threshold of 6 — so the long-function species flags it. The recorded fix
// extracts the product computation into a `products` helper, leaving both
// functions below the threshold while computing the identical result (proven by
// repo_test.go).
func Process(a, b, c, d int) int {
	x := a + b
	y := b + c
	z := c + d
	p := x * y
	q := y * z
	r := p + q
	return r
}
