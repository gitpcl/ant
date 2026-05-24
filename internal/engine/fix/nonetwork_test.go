package fix_test

import (
	"errors"
	"net/http"
	"testing"
)

// failingTransport records that a network attempt was made and refuses it. It
// lets a test assert that a code path (the deterministic fixer) performs no HTTP
// at all: if anything dials through http.DefaultClient/DefaultTransport, the
// flag flips and the round trip fails.
type failingTransport struct {
	dialed *bool
}

func (t failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	*t.dialed = true
	return nil, errors.New("network access is forbidden in this test")
}

// installNoNetworkGuard swaps http.DefaultTransport for a failing transport that
// flips *dialed if used, and returns a restore func. The deterministic fixer
// must complete without flipping the flag (it is a pure transform); a fixer that
// regressed into dialing the network would set it.
func installNoNetworkGuard(t *testing.T, dialed *bool) func() {
	t.Helper()
	prev := http.DefaultTransport
	http.DefaultTransport = failingTransport{dialed: dialed}
	return func() { http.DefaultTransport = prev }
}
