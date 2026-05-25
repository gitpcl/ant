#!/bin/sh
# Ant installer — `curl -fsSL https://.../install.sh | sh`
#
# Detects OS + arch, downloads the matching goreleaser release archive, VERIFIES
# its sha256 against the published checksums file, and installs the `ant` binary
# to a bin dir on PATH. A checksum mismatch ABORTS (non-zero) and installs
# nothing — supply-chain integrity is non-negotiable for a `curl | sh` flow.
#
# POSIX sh only (no bashisms): runs under dash/ash on a clean Debian/Alpine box.
#
# The OS/arch detection and checksum verification are factored into pure
# functions with no network side effects so they are unit-testable. Run
#   sh install.sh --self-test
# to exercise them offline: it proves a correct checksum proceeds and a tampered
# checksum aborts non-zero without writing a binary.

set -eu

# ---- configuration (overridable by env for testing/forks) -------------------
ANT_REPO="${ANT_REPO:-gitpcl/ant}"
ANT_VERSION="${ANT_VERSION:-latest}"
ANT_INSTALL_DIR="${ANT_INSTALL_DIR:-}"
# Base URL for release assets; overridable so the self-test / a local snapshot
# can point at a file:// or http://localhost source instead of GitHub.
ANT_BASE_URL="${ANT_BASE_URL:-https://github.com/${ANT_REPO}/releases}"

PROJECT_NAME="ant"

# ---- logging ----------------------------------------------------------------
log()  { printf '%s\n' "ant-install: $*" >&2; }
die()  { printf '%s\n' "ant-install: error: $*" >&2; exit 1; }

# ---- pure detection (testable) ----------------------------------------------

# detect_os maps `uname -s` to a goreleaser GOOS token. Unsupported -> non-zero.
detect_os() {
	os_raw="$1"
	case "$os_raw" in
		Linux)   printf 'linux' ;;
		Darwin)  printf 'darwin' ;;
		MINGW* | MSYS* | CYGWIN* | Windows_NT) printf 'windows' ;;
		*) return 1 ;;
	esac
}

# detect_arch maps `uname -m` to a goreleaser GOARCH token. The arm64 aliases
# (aarch64/arm64) are the Pi/Jetson case and MUST resolve. Unsupported -> 1.
detect_arch() {
	arch_raw="$1"
	case "$arch_raw" in
		x86_64 | amd64)       printf 'amd64' ;;
		aarch64 | arm64)      printf 'arm64' ;;
		*) return 1 ;;
	esac
}

# asset_ext returns the archive extension goreleaser uses for an OS.
asset_ext() {
	case "$1" in
		windows) printf 'zip' ;;
		*)       printf 'tar.gz' ;;
	esac
}

# asset_name builds the archive filename for (os, arch), matching the goreleaser
# name_template: ant_<os>_<arch>.<ext>. The version is deliberately NOT part of
# the filename — that is what lets GitHub's /latest/download/<name> redirect
# resolve to a stable name so install.sh can fetch the newest release without
# the caller pinning ANT_VERSION. MUST match .goreleaser.yaml archives
# name_template. The released binary still self-describes via `ant --version`.
# Args: os arch
asset_name() {
	_os="$1"; _arch="$2"
	printf '%s_%s_%s.%s' "$PROJECT_NAME" "$_os" "$_arch" "$(asset_ext "$_os")"
}

# checksums_name returns the checksums filename, matching goreleaser's
# checksum.name_template: ant_checksums.txt. Version-free for the same reason as
# asset_name() — /latest/download/ant_checksums.txt must resolve unversioned.
checksums_name() {
	printf '%s_checksums.txt' "$PROJECT_NAME"
}

# sha256_of prints the lowercase sha256 hex of a file, picking whichever tool is
# present. Fails if neither sha256sum nor shasum is available.
sha256_of() {
	_f="$1"
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$_f" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$_f" | awk '{print $1}'
	else
		die "need sha256sum or shasum to verify the download"
	fi
}

# expected_sum extracts the expected hash for an asset from a goreleaser
# checksums file. The file format is "<sha256>  <filename>" per line. Prints the
# hash on stdout; returns non-zero (and prints nothing) if the asset is absent.
# Args: checksums_file asset_filename
expected_sum() {
	_file="$1"; _asset="$2"
	# Match the exact basename in the second field; print the first field.
	awk -v want="$_asset" '$2 == want { print $1; found=1 } END { exit !found }' "$_file"
}

# verify_checksum compares the actual sha256 of a downloaded asset against the
# expected hash for it in the checksums file. Returns 0 on match, non-zero on
# mismatch or when the asset is not listed. This is the integrity gate.
# Args: asset_path checksums_file asset_filename
verify_checksum() {
	_asset_path="$1"; _checksums="$2"; _asset_name="$3"
	_want="$(expected_sum "$_checksums" "$_asset_name")" || {
		log "checksum: asset '$_asset_name' not present in checksums file"
		return 1
	}
	_got="$(sha256_of "$_asset_path")"
	if [ "$_want" != "$_got" ]; then
		log "checksum MISMATCH for $_asset_name"
		log "  expected: $_want"
		log "  actual:   $_got"
		return 1
	fi
	return 0
}

# ---- download ---------------------------------------------------------------

# fetch downloads url -> dest using curl or wget (whichever exists). Fails hard
# on HTTP errors so a 404 release asset does not get silently treated as data.
fetch() {
	_url="$1"; _dest="$2"
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$_url" -o "$_dest"
	elif command -v wget >/dev/null 2>&1; then
		wget -q "$_url" -O "$_dest"
	else
		die "need curl or wget to download releases"
	fi
}

# release_tag normalizes a pinned ANT_VERSION into the GitHub release tag form.
# Release tags are semver with a leading 'v' (e.g. v0.1.0), so accept both
# `0.1.0` and `v0.1.0` from the caller and always emit the v-prefixed tag.
release_tag() {
	case "$1" in
		v*) printf '%s' "$1" ;;
		*)  printf 'v%s' "$1" ;;
	esac
}

# release_url builds the download URL for an asset name under the configured
# base URL, handling the GitHub "latest" vs tagged path shapes. Asset names are
# version-free, so the same name is used for both shapes.
release_url() {
	_name="$1"
	if [ "$ANT_VERSION" = "latest" ]; then
		printf '%s/latest/download/%s' "$ANT_BASE_URL" "$_name"
	else
		printf '%s/download/%s/%s' "$ANT_BASE_URL" "$(release_tag "$ANT_VERSION")" "$_name"
	fi
}

# choose_install_dir picks a writable bin dir on PATH (or a sensible default).
choose_install_dir() {
	if [ -n "$ANT_INSTALL_DIR" ]; then
		printf '%s' "$ANT_INSTALL_DIR"
		return 0
	fi
	# Prefer a per-user dir that needs no sudo.
	for d in "$HOME/.local/bin" "/usr/local/bin"; do
		if [ -d "$d" ] && [ -w "$d" ]; then
			printf '%s' "$d"
			return 0
		fi
	done
	# Default: create the per-user dir.
	printf '%s' "$HOME/.local/bin"
}

# ---- main install flow ------------------------------------------------------
main_install() {
	os="$(detect_os "$(uname -s)")"   || die "unsupported OS: $(uname -s)"
	arch="$(detect_arch "$(uname -m)")" || die "unsupported architecture: $(uname -m)"
	log "detected platform: ${os}/${arch}"

	# Asset names are version-free (ant_<os>_<arch>.<ext>), so the SAME filenames
	# work for both "latest" and a pinned ANT_VERSION=x.y.z. release_url() picks
	# the GitHub path shape: /latest/download/<name> when ANT_VERSION=latest
	# (the default), or /download/<tag>/<name> for a pinned version. There is no
	# longer any need to know the version number to build a filename.
	if [ "$ANT_VERSION" = "latest" ]; then
		log "resolving latest release"
	else
		log "installing pinned version: $ANT_VERSION"
	fi

	asset="$(asset_name "$os" "$arch")"
	sums="$(checksums_name)"

	tmp="$(mktemp -d "${TMPDIR:-/tmp}/ant-install.XXXXXX")"
	trap 'rm -rf "$tmp"' EXIT INT TERM

	log "downloading $asset"
	fetch "$(release_url "$asset")" "$tmp/$asset" || die "failed to download $asset"
	log "downloading $sums"
	fetch "$(release_url "$sums")" "$tmp/$sums"   || die "failed to download $sums"

	log "verifying checksum"
	verify_checksum "$tmp/$asset" "$tmp/$sums" "$asset" || die "checksum verification failed — aborting, nothing installed"
	log "checksum OK"

	# Extract the binary.
	case "$asset" in
		*.tar.gz) tar -xzf "$tmp/$asset" -C "$tmp" ;;
		*.zip)    (command -v unzip >/dev/null 2>&1 || die "need unzip for windows archives"); unzip -q "$tmp/$asset" -d "$tmp" ;;
	esac

	bin="ant"
	[ "$os" = "windows" ] && bin="ant.exe"
	[ -f "$tmp/$bin" ] || die "archive did not contain $bin"

	dir="$(choose_install_dir)"
	mkdir -p "$dir"
	install -m 0755 "$tmp/$bin" "$dir/$bin" 2>/dev/null || { cp "$tmp/$bin" "$dir/$bin" && chmod 0755 "$dir/$bin"; }
	log "installed ant to $dir/$bin"

	case ":$PATH:" in
		*":$dir:"*) : ;;
		*) log "note: $dir is not on your PATH — add it to use 'ant' directly" ;;
	esac

	log "done. Run: $dir/$bin --help"
}

# ---- self-test (offline, no network) ----------------------------------------
# Proves the integrity gate: a correct checksum proceeds; a tampered checksum
# aborts non-zero without installing. Exits 0 only if BOTH properties hold.
self_test() {
	t="$(mktemp -d "${TMPDIR:-/tmp}/ant-selftest.XXXXXX")"
	# shellcheck disable=SC2064
	trap "rm -rf '$t'" EXIT

	fails=0
	check() { if [ "$1" = "$2" ]; then printf 'ok   - %s\n' "$3"; else printf 'FAIL - %s (got [%s] want [%s])\n' "$3" "$1" "$2"; fails=$((fails+1)); fi; }

	# Detection mapping.
	check "$(detect_os Linux)"            "linux"   "detect_os Linux"
	check "$(detect_os Darwin)"           "darwin"  "detect_os Darwin"
	check "$(detect_arch x86_64)"         "amd64"   "detect_arch x86_64"
	check "$(detect_arch aarch64)"        "arm64"   "detect_arch aarch64 (Pi/Jetson)"
	check "$(detect_arch arm64)"          "arm64"   "detect_arch arm64 (Apple/Pi)"
	if detect_os Plan9 2>/dev/null; then check "reached" "unreachable" "detect_os rejects unknown"; else check "rejected" "rejected" "detect_os rejects unknown OS"; fi
	if detect_arch mips 2>/dev/null;  then check "reached" "unreachable" "detect_arch rejects unknown"; else check "rejected" "rejected" "detect_arch rejects unknown arch"; fi

	# Asset/checksum filename templates (must match .goreleaser.yaml). Names are
	# VERSION-FREE so the same name resolves for both 'latest' and a pinned tag.
	check "$(asset_name linux arm64)"   "ant_linux_arm64.tar.gz"   "asset_name linux/arm64"
	check "$(asset_name windows amd64)" "ant_windows_amd64.zip"    "asset_name windows/amd64 -> zip"
	check "$(checksums_name)"           "ant_checksums.txt"        "checksums_name template"

	# URL resolution: 'latest' (the default, unpinned) uses /latest/download/<name>
	# with the version-free asset name; a pinned ANT_VERSION uses the v-tagged
	# /download/<tag>/<name> path with the SAME asset name. release_tag normalizes
	# a bare semver into the v-prefixed GitHub tag. ANT_VERSION is set in the
	# current shell (no subshell) so a FAIL still increments the parent's count.
	_saved_ver="$ANT_VERSION"
	_saved_base="$ANT_BASE_URL"
	ANT_BASE_URL="https://github.com/gitpcl/ant/releases"

	ANT_VERSION="latest"
	check "$(release_url "$(asset_name linux arm64)")" \
	      "https://github.com/gitpcl/ant/releases/latest/download/ant_linux_arm64.tar.gz" \
	      "release_url latest (unpinned) -> /latest/download/<version-free name>"

	ANT_VERSION="0.1.0"
	check "$(release_url "$(asset_name linux arm64)")" \
	      "https://github.com/gitpcl/ant/releases/download/v0.1.0/ant_linux_arm64.tar.gz" \
	      "release_url pinned 0.1.0 -> /download/v0.1.0/<same name>"

	ANT_VERSION="v0.1.0"
	check "$(release_url "$(asset_name linux arm64)")" \
	      "https://github.com/gitpcl/ant/releases/download/v0.1.0/ant_linux_arm64.tar.gz" \
	      "release_url pinned v0.1.0 (already v-prefixed) -> /download/v0.1.0/<same name>"

	ANT_VERSION="$_saved_ver"
	ANT_BASE_URL="$_saved_base"

	# Build a fake asset + a valid checksums file over it.
	asset="ant_linux_arm64.tar.gz"
	printf 'fake-ant-binary-archive\n' > "$t/$asset"
	good="$(sha256_of "$t/$asset")"
	printf '%s  %s\n' "$good" "$asset" > "$t/sums-good.txt"
	# A tampered checksums file: same asset name, wrong hash.
	printf '%s  %s\n' "0000000000000000000000000000000000000000000000000000000000000000" "$asset" > "$t/sums-bad.txt"
	# A checksums file that does not list the asset at all.
	printf '%s  %s\n' "$good" "some_other_file.tar.gz" > "$t/sums-missing.txt"

	# Correct checksum -> verify_checksum returns 0 (proceeds).
	if verify_checksum "$t/$asset" "$t/sums-good.txt" "$asset" >/dev/null 2>&1; then
		check "proceed" "proceed" "correct checksum -> proceeds (exit 0)"
	else
		check "abort" "proceed" "correct checksum -> proceeds (exit 0)"
	fi

	# Tampered checksum -> verify_checksum returns non-zero (aborts).
	if verify_checksum "$t/$asset" "$t/sums-bad.txt" "$asset" >/dev/null 2>&1; then
		check "proceed" "abort" "tampered checksum -> aborts (non-zero)"
	else
		check "abort" "abort" "tampered checksum -> aborts (non-zero)"
	fi

	# Asset absent from checksums -> non-zero (fail closed, never trust unlisted).
	if verify_checksum "$t/$asset" "$t/sums-missing.txt" "$asset" >/dev/null 2>&1; then
		check "proceed" "abort" "unlisted asset -> aborts (non-zero)"
	else
		check "abort" "abort" "unlisted asset -> aborts (fail closed)"
	fi

	# Prove the full top-level guard exits non-zero on a tampered sum: run a
	# subshell that mimics the install gate and confirm no binary is "installed".
	installed_marker="$t/installed"
	rc=0
	(
		set -e
		verify_checksum "$t/$asset" "$t/sums-bad.txt" "$asset" || exit 7
		: > "$installed_marker"   # only reached if verification wrongly passes
	) >/dev/null 2>&1 || rc=$?
	check "$rc" "7" "tampered checksum -> install gate exits non-zero (rc=7)"
	if [ -e "$installed_marker" ]; then
		check "installed" "not-installed" "tampered checksum -> NOTHING installed"
	else
		check "not-installed" "not-installed" "tampered checksum -> NOTHING installed"
	fi

	printf '\n'
	if [ "$fails" -eq 0 ]; then
		printf 'self-test: PASS\n'
		return 0
	fi
	printf 'self-test: FAIL (%d)\n' "$fails"
	return 1
}

# ---- entrypoint -------------------------------------------------------------
case "${1:-}" in
	--self-test) self_test ;;
	--help|-h)
		printf 'Usage: sh install.sh                 # installs the LATEST release (default)\n'
		printf '       curl -fsSL https://raw.githubusercontent.com/gitpcl/ant/main/install.sh | sh\n'
		printf '\n'
		printf 'Environment (all optional):\n'
		printf '  ANT_VERSION       release to install; defaults to "latest". Pin with\n'
		printf '                    ANT_VERSION=v0.1.0 (or 0.1.0) to install a specific tag.\n'
		printf '  ANT_INSTALL_DIR   bin dir to install into (default: a writable PATH dir,\n'
		printf '                    else $HOME/.local/bin).\n'
		printf '  ANT_REPO          owner/repo to pull releases from (default: gitpcl/ant).\n'
		printf '\n'
		printf '       sh install.sh --self-test   # offline integrity self-check\n'
		;;
	*) main_install ;;
esac
