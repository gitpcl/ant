package insecurerandom

import (
	"encoding/hex"
	"testing"
)

// TestSessionTokenShape exercises the POST-FIX shape: after the insecure-random
// fix SessionToken reads from crypto/rand and hex-encodes 16 bytes, so the token
// is 32 hex characters and decodes cleanly. tests:affected runs this against the
// PATCHED scratch tree, so it asserts the crypto/rand rewrite produces a
// well-formed token of the expected length.
func TestSessionTokenShape(t *testing.T) {
	tok := SessionToken()
	if len(tok) != 32 {
		t.Fatalf("SessionToken() length = %d, want 32 hex chars (16 bytes)", len(tok))
	}
	if _, err := hex.DecodeString(tok); err != nil {
		t.Fatalf("SessionToken() = %q is not valid hex: %v", tok, err)
	}
}

// TestSessionTokenUnpredictable confirms two successive tokens differ — a sanity
// check that the value is drawn from a real entropy source, not a constant.
func TestSessionTokenUnpredictable(t *testing.T) {
	if SessionToken() == SessionToken() {
		t.Fatal("two SessionToken() calls returned the same value — the RNG is not producing fresh randomness")
	}
}
