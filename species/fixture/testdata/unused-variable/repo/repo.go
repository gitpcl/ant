package unusedvariable

// scaleFactor is the package's real, referenced state — it has no marker and is
// read by Scale below, so the species leaves it alone (and the compile gate
// would skip its removal anyway).
var scaleFactor = 3

//ant:unused-variable scratch was a leftover constant nothing reads anymore
var unusedScratch = 41 + 1

// Scale is the package's real exported logic; it reads scaleFactor, proving the
// fix removes ONLY the marked, unreferenced declaration and keeps the tree
// building. After the unused-variable fix deletes unusedScratch, `compile` still
// passes and `detector-clears` reports zero matches.
func Scale(n int) int {
	return n * scaleFactor
}
