package deadcode

// Live is the package's real, exported logic — it stays.
func Live(n int) int {
	return n + 1
}

//ant:dead-code unreferenced unexported helper kept only as dead weight
func deadHelper(n int) int {
	return n * 999
}
