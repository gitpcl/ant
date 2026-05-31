package local

import (
	"os"
	"testing"
)

// TestMain points the user-local trust root (ANT_TRUST_HOME) at a throwaway
// directory for the whole package's tests, so the trust-persistence tests are
// hermetic — they never read or write the real os.UserConfigDir() tree. Each
// test still uses a distinct base TempDir, so their per-repo trust files (keyed
// by hash of the absolute base path) never collide under this shared root.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ant-trust-home-")
	if err != nil {
		panic("store test setup: cannot create temp trust home: " + err.Error())
	}
	if err := os.Setenv(trustHomeEnv, dir); err != nil {
		panic("store test setup: cannot set " + trustHomeEnv + ": " + err.Error())
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
