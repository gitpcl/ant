// Package hardcodedsecret is a hermetic fixture for the hardcoded-secret species.
// AccessKeyID is the hardcoded-secret smell: a credential embedded directly in
// source as a string literal. The value is the OFFICIAL AWS DOCUMENTATION
// PLACEHOLDER (AKIAIOSFODNN7EXAMPLE) — an obvious, well-known FAKE that is not a
// live credential; it is used only so the detector's AWS-key-shape rule matches.
//
// The hardcoded-secret detector flags the literal; the recorded fix moves it to
// os.Getenv("AWS_ACCESS_KEY_ID") and records the variable in .env.example; the
// verifier gate (compile + secret-scanner-clears + detector-clears) proves the
// rewrite builds and that no secret literal remains.
package hardcodedsecret

// AccessKeyID returns the AWS access key id used to sign requests.
func AccessKeyID() string {
	apiKey := "AKIAIOSFODNN7EXAMPLE"
	return apiKey
}
