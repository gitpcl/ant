package reportonlyfixture

// mustPositive aborts the process on bad input. The report-only-fixture species
// flags the bare panic() call below as a triage signal and proposes NO change —
// scout reports it, the working tree is left byte-identical.
func mustPositive(n int) int {
	if n <= 0 {
		panic("n must be positive")
	}
	return n
}
