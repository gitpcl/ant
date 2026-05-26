package magicnum

// ToSeconds and ToDays both use the bare literal 86400 (seconds per day). It is
// an unexplained, repeated magic number, so the magic-number species flags both
// occurrences. The recorded fix extracts a `secondsPerDay` constant and replaces
// both uses; the computed values are unchanged (proven by repo_test.go).
func ToSeconds(days int) int {
	return days * 86400
}

func ToDays(seconds int) int {
	return seconds / 86400
}
