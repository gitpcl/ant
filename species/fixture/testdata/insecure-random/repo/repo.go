// Package insecurerandom is a hermetic fixture for the insecure-random species:
// SessionToken builds a SECURITY-SENSITIVE value (a session token) from math/rand,
// a predictable PRNG. The detector nominates the rand.Intn call; the recorded fix
// swaps math/rand for crypto/rand so the token is cryptographically unpredictable.
package insecurerandom

import (
	"fmt"
	"math/rand"
)

// SessionToken returns a 32-hex-char session token.
func SessionToken() string {
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(rand.Intn(256))
	}
	return fmt.Sprintf("%x", b)
}
