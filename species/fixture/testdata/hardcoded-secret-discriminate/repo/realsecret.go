// This file is the MATCH and the no-false-negative proof: a genuine
// mixed-case, high-entropy random literal (entropy ~5.0 bits/char) assigned to
// a credential-named `apiKey` variable. It is NOT a pure UPPER_SNAKE token and
// NOT a lowercase dotted path, so neither Rule 2 exclusion fires; it MUST be
// flagged. The value is a synthetic random string, not a live credential.
package discriminate

// apiKeySecret holds a (fake) high-entropy credential — this is the smell.
func apiKeySecret() string {
	apiKey := "wQ8xZ2pL9vK3mN7rT5yB1cF4hJ6sD0aG"
	return apiKey
}
