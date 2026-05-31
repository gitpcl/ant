package selfupdate

import (
	"slices"
	"strings"
	"testing"
)

// TestBuildEnv covers the option→env mapping the installer reads: an empty
// Version defaults to "latest", and empty InstallDir/Repo are omitted so the
// installer's own defaults apply.
func TestBuildEnv(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/home/me"}

	tests := []struct {
		name   string
		opts   Options
		want   []string // env entries that MUST be present
		absent []string // env keys that must NOT be present
	}{
		{
			name:   "defaults to latest, omits empty dir/repo",
			opts:   Options{},
			want:   []string{"ANT_VERSION=latest"},
			absent: []string{"ANT_INSTALL_DIR=", "ANT_REPO="},
		},
		{
			name: "pinned version and dir and repo",
			opts: Options{Version: "v0.3.0", InstallDir: "/opt/bin", Repo: "gitpcl/ant"},
			want: []string{"ANT_VERSION=v0.3.0", "ANT_INSTALL_DIR=/opt/bin", "ANT_REPO=gitpcl/ant"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := buildEnv(base, tc.opts)

			// Base env is preserved.
			for _, b := range base {
				if !slices.Contains(env, b) {
					t.Errorf("base env entry %q was dropped", b)
				}
			}
			for _, w := range tc.want {
				if !slices.Contains(env, w) {
					t.Errorf("missing env entry %q in %v", w, env)
				}
			}
			for _, prefix := range tc.absent {
				for _, e := range env {
					if strings.HasPrefix(e, prefix) {
						t.Errorf("env entry %q should be absent but found %q", prefix, e)
					}
				}
			}
		})
	}
}

// TestBuildEnvDoesNotMutateBase guards against the append-aliasing bug where
// appending to a copy could clobber the caller's slice backing array.
func TestBuildEnvDoesNotMutateBase(t *testing.T) {
	base := []string{"PATH=/usr/bin"}
	_ = buildEnv(base, Options{Version: "v0.3.0"})
	if len(base) != 1 || base[0] != "PATH=/usr/bin" {
		t.Fatalf("buildEnv mutated the caller's base env: %v", base)
	}
}
