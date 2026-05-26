package resourceleak

import (
	"io"
	"os"
)

// CountBytes is the resource-leak smell with MULTIPLE return paths: it opens a
// file with os.Open but never closes it. On the io.ReadAll error path AND on the
// success path the *os.File leaks — there is no Close anywhere in the function.
// The resource-leak species nominates the function (has os.Open, no Close), the
// recorded fix adds `defer f.Close()` immediately after the open so the file is
// closed on ALL return paths (the signature requirement), and the verifier gate
// (compile + tests:affected + detector-clears) confirms it.
func CountBytes(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}
