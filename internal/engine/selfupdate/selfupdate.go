// Package selfupdate implements `ant update`. It re-runs the official installer
// to fetch and atomically install the latest (or a pinned) release.
//
// Reusing install.sh is deliberate: the OS/arch detection, SHA-256 checksum
// verification, archive extraction, and atomic install-to-PATH logic live in ONE
// place. The update path and the first-time install path therefore cannot drift,
// and `ant update` inherits the installer's checksum gate for free — a corrupted
// or tampered release asset aborts the update before anything is replaced.
package selfupdate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/gitpcl/ant/internal/engine"
)

// DefaultScriptURL is the canonical installer on the project's default branch.
// It is the same script the docs tell users to curl, so `ant update` and a fresh
// install run identical code.
const DefaultScriptURL = "https://raw.githubusercontent.com/gitpcl/ant/main/install.sh"

// downloadTimeout bounds fetching the installer script (not the release asset,
// which the installer fetches itself).
const downloadTimeout = 30 * time.Second

// Options configures an update run. The zero value updates to the latest release,
// letting the installer pick a writable bin dir on PATH.
type Options struct {
	// Version is the release to install: "" or "latest" installs the newest; a
	// pinned tag like "v0.3.0" (or "0.3.0") installs that exact release.
	Version string
	// InstallDir is the target bin dir; "" lets the installer choose a writable
	// PATH dir (matching a first-time install).
	InstallDir string
	// ScriptURL overrides the installer location; "" uses DefaultScriptURL.
	ScriptURL string
	// Repo overrides the owner/repo releases are pulled from; "" uses the
	// installer default (gitpcl/ant).
	Repo string
}

// Run downloads the installer to a temp file and executes it with `sh`, streaming
// its output to out. The installer verifies the release checksum before replacing
// the binary, so a bad download aborts the update. It returns a typed operational
// error on an unsupported platform, a failed download, or a non-zero installer
// exit.
func Run(ctx context.Context, opts Options, out io.Writer) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("%w: `ant update` uses the POSIX installer and is not supported on Windows — download the release archive from the releases page instead", engine.ErrOperational)
	}

	scriptURL := opts.ScriptURL
	if scriptURL == "" {
		scriptURL = DefaultScriptURL
	}

	scriptPath, cleanup, err := downloadScript(ctx, scriptURL)
	if err != nil {
		return err
	}
	defer cleanup()

	// Run the installer via `sh <file>` (NOT a blind curl|sh pipe): the script is
	// on disk and inspectable, and we control its exact env. The installer's own
	// checksum verification is the integrity gate.
	cmd := exec.CommandContext(ctx, "sh", scriptPath)
	cmd.Env = buildEnv(os.Environ(), opts)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: installer failed: %v", engine.ErrOperational, err)
	}
	return nil
}

// buildEnv layers the update options onto the base environment as the env vars
// install.sh reads (ANT_VERSION / ANT_INSTALL_DIR / ANT_REPO). Empty options are
// omitted so the installer's own defaults apply. Kept pure for testability.
func buildEnv(base []string, opts Options) []string {
	env := append([]string(nil), base...)
	version := opts.Version
	if version == "" {
		version = "latest"
	}
	env = append(env, "ANT_VERSION="+version)
	if opts.InstallDir != "" {
		env = append(env, "ANT_INSTALL_DIR="+opts.InstallDir)
	}
	if opts.Repo != "" {
		env = append(env, "ANT_REPO="+opts.Repo)
	}
	return env
}

// downloadScript fetches the installer to a temp file and returns its path plus a
// cleanup func. The caller runs the file with `sh`; a failed HTTP status is a hard
// error so a 404 never gets executed as a shell script.
func downloadScript(ctx context.Context, url string) (path string, cleanup func(), err error) {
	reqCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, fmt.Errorf("%w: build installer request: %v", engine.ErrOperational, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("%w: download installer: %v", engine.ErrOperational, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("%w: download installer: unexpected status %s", engine.ErrOperational, resp.Status)
	}

	f, err := os.CreateTemp("", "ant-update-*.sh")
	if err != nil {
		return "", nil, fmt.Errorf("%w: create temp installer: %v", engine.ErrOperational, err)
	}
	cleanup = func() { _ = os.Remove(f.Name()) }
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		cleanup()
		return "", nil, fmt.Errorf("%w: write temp installer: %v", engine.ErrOperational, err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("%w: close temp installer: %v", engine.ErrOperational, err)
	}
	return f.Name(), cleanup, nil
}
