package missingawait

// square is the per-item work each goroutine performs.
func square(n int) int { return n * n }

// SumSquares is the missing-await smell. Go has no `await`; the closest
// meaningful Go analogue is a goroutine whose result is never waited on. Here
// each iteration fires a `go func(){...}()` that mutates `total` and is never
// synchronized: the function returns before the goroutines run (their results
// are dropped) AND the concurrent writes to `total` race. The missing-await
// species nominates the un-awaited fire-and-forget goroutine in the loop; the
// recorded fix collects results into a per-index slice, waits on a WaitGroup,
// then sums — and the verifier gate (compile + tests:affected + detector-clears,
// the suite runs with -race) confirms the result is now correct and awaited.
func SumSquares(nums []int) int {
	var total int
	for _, n := range nums {
		go func(n int) {
			total += square(n)
		}(n)
	}
	return total
}
