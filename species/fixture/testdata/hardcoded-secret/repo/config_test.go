package hardcodedsecret

import "testing"

// TestAccessKeyIDFromEnv exercises the POST-FIX shape: after the hardcoded-secret
// fix, AccessKeyID reads the credential from the AWS_ACCESS_KEY_ID environment
// variable instead of a hardcoded literal. tests:affected runs this against the
// PATCHED scratch tree, so it asserts the value is sourced from the environment.
//
// The sentinel value below is intentionally a short, low-entropy, non-token-
// shaped string so the secret scanner / detector do NOT flag the test file
// itself (a test legitimately injects a placeholder; only a real credential
// baked into non-test source is the smell).
func TestAccessKeyIDFromEnv(t *testing.T) {
	const sentinel = "env-supplied"
	t.Setenv("AWS_ACCESS_KEY_ID", sentinel)
	if got := AccessKeyID(); got != sentinel {
		t.Fatalf("AccessKeyID() = %q, want the value from AWS_ACCESS_KEY_ID", got)
	}
}

// TestAccessKeyIDEmptyWhenUnset confirms the value comes from the environment:
// with the variable unset, AccessKeyID returns empty (no value is baked into the
// binary anymore — the secret is no longer hardcoded).
func TestAccessKeyIDEmptyWhenUnset(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	if got := AccessKeyID(); got != "" {
		t.Fatalf("AccessKeyID() = %q, want empty when AWS_ACCESS_KEY_ID is unset (the value must not be hardcoded)", got)
	}
}
