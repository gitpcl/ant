package unsafeconcurrency

// CountUp is the unsafe-concurrency smell: it spawns `n` goroutines that each
// increment the shared `count` variable with NO synchronization — the writes
// race, and the function returns `count` without waiting for the goroutines, so
// the result is both racy and almost always wrong. There is no `sync` primitive
// anywhere in the function. The unsafe-concurrency species nominates the function
// (has `go func`, no `sync.`), the recorded fix adds a sync.Mutex guarding the
// increment and a sync.WaitGroup owning the goroutines' lifecycle, and the
// verifier gate (compile + tests:affected + detector-clears) confirms it. CI runs
// the suite under `go test -race`, which proves the fixed code is race-free.
func CountUp(n int) int {
	count := 0
	for i := 0; i < n; i++ {
		go func() {
			count++
		}()
	}
	return count
}
