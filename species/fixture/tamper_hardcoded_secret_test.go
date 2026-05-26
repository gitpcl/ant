package fixture_test

// Tamper guard for the hardcoded-secret scanner-clears gate (Sprint 021 P6,
// @security). The fixture test proves the POSITIVE (the genuine multi-file fix
// clears the scanner); this proves the NEGATIVE that makes the gate a remediation
// proof rather than theater: a "fix" that does NOT remove the secret literal is
// REJECTED by the command: secret-scanner-clears verifier, and the genuine fix is
// accepted by the SAME gate (the control). A regression that weakened the scanner
// (so a leftover secret slipped through) would fail here.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
	"github.com/gitpcl/ant/internal/engine/species"
	"github.com/gitpcl/ant/internal/engine/verify"
)

// A tamper patch that edits .env.example but leaves config.go's secret literal in
// place — a remediation that only LOOKS done. The scanner-clears gate must reject
// it because the secret still exists in the post-fix tree.
const tamperEnvOnlyPatch = `--- a/.env.example
+++ b/.env.example
@@ -5,1 +5,2 @@
 APP_NAME=hardcodedsecret-fixture
+AWS_ACCESS_KEY_ID=
`

func TestHardcodedSecretScannerRejectsLeftoverSecret(t *testing.T) {
	// Resolve the scanner script to an absolute path BEFORE chdir (relative paths
	// would break once cwd moves into the fixture repo).
	scriptPath, err := filepath.Abs(filepath.Join("..", "hardcoded-secret", "scan.sh"))
	if err != nil {
		t.Fatalf("resolve scan.sh: %v", err)
	}
	repo := filepath.Join("testdata", "hardcoded-secret", "repo")
	t.Chdir(repo)

	// Build ONLY the command: secret-scanner verifier (the remediation proof) and
	// run it over a scratch copy with the TAMPER diff applied.
	scanner := verify.NewCommandVerifier("command:scan.sh", species.DefaultScriptInterpreter, scriptPath)

	tamper := engine.ProposedDiff{
		Files: []engine.FileDiff{{Path: ".env.example", Patch: tamperEnvOnlyPatch}},
		Fixer: "tamper (test)",
	}
	res := scanner.Verify(context.Background(), tamper, engine.Scope{Root: "."})
	if res.Passed {
		t.Fatal("scanner-clears gate PASSED a fix that left the secret in config.go — the remediation proof is broken")
	}
	t.Logf("tamper correctly rejected: %v", res.Checks)

	// CONTROL: the genuine multi-file fix (the recorded golden patches) MUST clear
	// the scanner — proving the gate is not simply always-failing.
	good := engine.ProposedDiff{
		Files: []engine.FileDiff{
			{Path: ".env.example", Patch: hardcodedSecretEnvPatch},
			{Path: "config.go", Patch: hardcodedSecretConfigPatch},
		},
		Fixer: "recorded (control)",
	}
	if ok := scanner.Verify(context.Background(), good, engine.Scope{Root: "."}); !ok.Passed {
		t.Fatalf("scanner-clears gate REJECTED the genuine fix — the gate is over-strict: %v", ok.Checks)
	}
}
