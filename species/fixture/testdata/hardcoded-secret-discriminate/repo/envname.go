// Package discriminate exercises the hardcoded-secret Rule 2 value-shape
// EXCLUSIONS. This file is a NON-MATCH: the literal assigned to the
// credential-named `apiKey` variable is an ENV-VAR NAME (a pure all-uppercase
// SNAKE_CASE identifier), i.e. the reference-the-env-var pattern — the OPPOSITE
// of a leaked value. It clears the length(>=20) and entropy(>=3.5) gate
// (entropy ~3.75 bits/char), so it reaches the exclusion; the
// `^[A-Z][A-Z0-9_]*$` env-var-name shape excludes it. It must NOT be flagged.
package discriminate

// apiKeyEnv names the environment variable the API key is read from.
const apiKey = "ANT_RAWMODEL_API_KEY"

// APIKeyEnv returns the env-var NAME (not a secret value).
func APIKeyEnv() string { return apiKey }
