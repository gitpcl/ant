package todoexp

// TODO(2019-01-01): remove this shim once the old API is gone.
func Shim() int {
	return legacy()
}

// FIXME(#123): this leaks a goroutine on cancel.
func Worker() {}

// HACK: temporary workaround for the broken upstream parser.
func Parse(s string) string { return s }

// TODO: a perfectly normal forward-looking note with no date or issue reference.
// A bare marker like this carries no staleness signal, so it stays unflagged.
func Future() {}

func legacy() int { return 0 }
