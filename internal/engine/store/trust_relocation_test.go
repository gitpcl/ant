package local

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gitpcl/ant/internal/engine/species"
)

// TestTrustStateLivesOutsideScannedRepo proves the Sprint 022 security fix: trust
// state is persisted under the user-local trust root, NOT inside the scanned
// repo, so a `MarkReviewed` never writes anything into the target tree's
// .ant/state. A repo the tool merely scans must not gain a trust file it could
// later ship to another machine.
func TestTrustStateLivesOutsideScannedRepo(t *testing.T) {
	base := t.TempDir()
	st := New(base)
	if err := st.MarkReviewed("some-species"); err != nil {
		t.Fatalf("MarkReviewed: %v", err)
	}

	// The legacy in-repo location must NOT exist.
	legacy := filepath.Join(base, stateDir, trustFileName)
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("trust state leaked into the scanned repo at %s (err=%v); it must live in the user-local root", legacy, err)
	}

	// And the round-trip still works via the user-local root.
	got, err := st.TrustFor("some-species")
	if err != nil {
		t.Fatalf("TrustFor: %v", err)
	}
	if !got.Reviewed {
		t.Fatal("MarkReviewed did not persist through the user-local trust root")
	}
}

// TestRepoSuppliedTrustFileIsIgnored proves the trust-confusion vector is closed:
// a species-trust.json planted inside the scanned repo (the foreign-checkout
// attack from the security review) is NOT consulted. LoadTrust reads only the
// user-local file keyed by the repo's absolute path.
func TestRepoSuppliedTrustFileIsIgnored(t *testing.T) {
	base := t.TempDir()

	// An attacker-controlled repo pre-asserts reviewed=true for its own species.
	planted := map[string]species.TrustState{"evil": {Seen: true, Reviewed: true}}
	stateDirPath := filepath.Join(base, stateDir)
	if err := os.MkdirAll(stateDirPath, dirPerm); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, _ := json.Marshal(planted)
	if err := os.WriteFile(filepath.Join(stateDirPath, trustFileName), data, filePerm); err != nil {
		t.Fatalf("plant trust file: %v", err)
	}

	// LoadTrust must ignore the planted file — the species is still brand new.
	got, err := New(base).TrustFor("evil")
	if err != nil {
		t.Fatalf("TrustFor: %v", err)
	}
	if got.Reviewed || got.Seen {
		t.Fatalf("repo-supplied trust file was honored (%+v); a scanned repo must not self-assert trust", got)
	}
	if !got.FreshlyInstalled() {
		t.Fatal("a species the user never reviewed on THIS machine must read as freshly installed (propose-only)")
	}
}

// TestTrustKeyedByAbsolutePath proves `ant fix .` and `ant fix <abs>` over the
// same tree share trust (same key), while a different repo gets its own file.
func TestTrustKeyedByAbsolutePath(t *testing.T) {
	repo := t.TempDir()
	if err := New(repo).MarkReviewed("s"); err != nil {
		t.Fatalf("MarkReviewed: %v", err)
	}

	// A relative path that resolves to the same tree sees the same trust.
	t.Chdir(repo)
	dotGot, err := New(".").TrustFor("s")
	if err != nil {
		t.Fatalf("TrustFor(.): %v", err)
	}
	if !dotGot.Reviewed {
		t.Error(`New(".") and New(abs) over the same repo must share trust state`)
	}

	// A different repo must not inherit it.
	other, err := New(t.TempDir()).TrustFor("s")
	if err != nil {
		t.Fatalf("TrustFor(other): %v", err)
	}
	if other.Reviewed {
		t.Error("a different repo wrongly inherited another repo's trust state")
	}
}
