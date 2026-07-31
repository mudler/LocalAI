#!/bin/sh
# LocalAI installer.
#
#   curl -sSL https://localai.io/install.sh | sh
#
# Downloads the release binary that matches this machine, checks it against the
# checksums published with the release, and puts it on your PATH. Nothing is
# built, nothing is left behind, and running it twice is a no-op.
#
# Environment:
#   LOCALAI_VERSION   pin a release, for example v4.7.1 (default: latest)
#   INSTALL_DIR       where to put the binary (default: /usr/local/bin,
#                     falling back to ~/.local/bin when that is not writable)
#   LOCALAI_FORCE     set to 1 to reinstall even if the same build is present
#   LOCALAI_NO_SUDO   set to 1 to never escalate, use ~/.local/bin instead
#   GITHUB_TOKEN      optional, only used to avoid GitHub API rate limits

set -eu

REPO="mudler/LocalAI"
API="https://api.github.com/repos/${REPO}/releases"
DL="https://github.com/${REPO}/releases/download"
BIN="local-ai"
TMPDIR_LOCALAI=""

say() { printf '%s\n' "$*"; }
step() { printf '  %s\n' "$*"; }

die() {
	printf '\nlocalai install: %s\n' "$1" >&2
	if [ $# -gt 1 ]; then
		printf '%s\n' "$2" >&2
	fi
	exit 1
}

cleanup() {
	if [ -n "$TMPDIR_LOCALAI" ] && [ -d "$TMPDIR_LOCALAI" ]; then
		rm -rf "$TMPDIR_LOCALAI"
	fi
}
trap cleanup EXIT INT TERM HUP

have() { command -v "$1" >/dev/null 2>&1; }

# --- fetching ---------------------------------------------------------------

fetch_to_stdout() {
	if have curl; then
		if [ -n "${GITHUB_TOKEN:-}" ]; then
			curl -fsSL -H "Authorization: Bearer ${GITHUB_TOKEN}" "$1"
		else
			curl -fsSL "$1"
		fi
	elif have wget; then
		if [ -n "${GITHUB_TOKEN:-}" ]; then
			wget -qO- --header="Authorization: Bearer ${GITHUB_TOKEN}" "$1"
		else
			wget -qO- "$1"
		fi
	else
		die "neither curl nor wget is installed" \
			"Install one of them and run this again, or download the binary by hand from https://github.com/${REPO}/releases"
	fi
}

# fetch_to_file URL DEST. Returns non-zero instead of exiting, so optional
# files (the checksum list) can be missing without failing the install.
fetch_to_file() {
	if have curl; then
		curl -fsSL --retry 3 --retry-delay 1 -o "$2" "$1"
	elif have wget; then
		wget -q --tries=3 -O "$2" "$1"
	else
		die "neither curl nor wget is installed" \
			"Install one of them and run this again, or download the binary by hand from https://github.com/${REPO}/releases"
	fi
}

# --- platform ---------------------------------------------------------------

detect_platform() {
	uname_s="$(uname -s 2>/dev/null || echo unknown)"
	uname_m="$(uname -m 2>/dev/null || echo unknown)"

	case "$uname_s" in
		Linux) OS="linux" ;;
		Darwin) OS="darwin" ;;
		*)
			die "unsupported operating system: ${uname_s}" \
				"LocalAI publishes binaries for Linux and macOS. On Windows, use WSL2 or the container image: docker run -p 8080:8080 localai/localai:latest"
			;;
	esac

	case "$uname_m" in
		x86_64 | amd64) ARCH="amd64" ;;
		aarch64 | arm64) ARCH="arm64" ;;
		*)
			die "unsupported CPU architecture: ${uname_m}" \
				"LocalAI publishes amd64 and arm64 binaries. Build from source instead: https://localai.io/docs/getting-started/build/"
			;;
	esac

	if [ "$OS" = "darwin" ] && [ "$ARCH" = "amd64" ]; then
		die "there is no Intel macOS binary in the LocalAI releases" \
			"On an Intel Mac, use the container image (docker run -p 8080:8080 localai/localai:latest) or build from source: https://localai.io/docs/getting-started/build/"
	fi
}

# --- version ----------------------------------------------------------------

resolve_version() {
	if [ -n "${LOCALAI_VERSION:-}" ]; then
		VERSION="$LOCALAI_VERSION"
		case "$VERSION" in
			v*) ;;
			*) VERSION="v${VERSION}" ;;
		esac
		return 0
	fi

	VERSION="$(fetch_to_stdout "${API}/latest" 2>/dev/null |
		sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' |
		head -n 1)"

	if [ -z "$VERSION" ]; then
		die "could not work out the latest LocalAI release" \
			"The GitHub API may be rate limiting you. Pin a version instead, for example: LOCALAI_VERSION=v4.7.1 sh install.sh"
	fi
}

# --- checksums --------------------------------------------------------------

sha256_of() {
	if have sha256sum; then
		sha256sum "$1" | awk '{print $1}'
	elif have shasum; then
		shasum -a 256 "$1" | awk '{print $1}'
	elif have openssl; then
		openssl dgst -sha256 "$1" | awk '{print $NF}'
	else
		return 1
	fi
}

# --- install target ---------------------------------------------------------

# Sets TARGET_DIR and SUDO ("" or "sudo").
choose_target() {
	SUDO=""

	if [ -n "${INSTALL_DIR:-}" ]; then
		TARGET_DIR="$INSTALL_DIR"
		mkdir -p "$TARGET_DIR" 2>/dev/null || die "cannot create ${TARGET_DIR}" \
			"Pick a directory you can write to, for example: INSTALL_DIR=\$HOME/.local/bin sh install.sh"
		[ -w "$TARGET_DIR" ] || die "cannot write to ${TARGET_DIR}" \
			"Pick a directory you can write to, for example: INSTALL_DIR=\$HOME/.local/bin sh install.sh"
		return 0
	fi

	TARGET_DIR="/usr/local/bin"

	if mkdir -p "$TARGET_DIR" 2>/dev/null && [ -w "$TARGET_DIR" ]; then
		return 0
	fi

	if [ "${LOCALAI_NO_SUDO:-0}" != "1" ] && have sudo; then
		SUDO="sudo"
		return 0
	fi

	TARGET_DIR="${HOME}/.local/bin"
	mkdir -p "$TARGET_DIR" 2>/dev/null || die "cannot create ${TARGET_DIR}" \
		"Set INSTALL_DIR to somewhere writable and run this again."
	[ -w "$TARGET_DIR" ] || die "cannot write to ${TARGET_DIR}" \
		"Set INSTALL_DIR to somewhere writable and run this again."
}

on_path() {
	case ":${PATH}:" in
		*":$1:"*) return 0 ;;
		*) return 1 ;;
	esac
}

# --- main -------------------------------------------------------------------

say "LocalAI installer"

detect_platform
step "platform      ${OS}/${ARCH}"

resolve_version
step "release       ${VERSION}"

ASSET="${BIN}-${VERSION}-${OS}-${ARCH}"
SUMS="LocalAI-${VERSION}-checksums.txt"

choose_target
step "install to    ${TARGET_DIR}${SUDO:+ (via sudo)}"

TMPDIR_LOCALAI="$(mktemp -d 2>/dev/null || mktemp -d -t localai)" ||
	die "could not create a temporary directory"

# Expected checksum first: it tells us whether an existing install is already
# the exact build we are about to fetch, so a second run downloads nothing.
EXPECTED=""
if fetch_to_file "${DL}/${VERSION}/${SUMS}" "${TMPDIR_LOCALAI}/${SUMS}" 2>/dev/null; then
	EXPECTED="$(awk -v f="$ASSET" '$2 == f || $2 == "*" f {print $1}' "${TMPDIR_LOCALAI}/${SUMS}" | head -n 1)"
fi

if [ -n "$EXPECTED" ] && [ "${LOCALAI_FORCE:-0}" != "1" ] && [ -x "${TARGET_DIR}/${BIN}" ]; then
	CURRENT="$(sha256_of "${TARGET_DIR}/${BIN}" 2>/dev/null || true)"
	if [ -n "$CURRENT" ] && [ "$CURRENT" = "$EXPECTED" ]; then
		step "already at    ${VERSION}, nothing to do"
		say ""
		say "Run it with:  ${BIN} run qwen3.5-35b-a3b-apex"
		exit 0
	fi
fi

step "downloading   ${ASSET}"
if ! fetch_to_file "${DL}/${VERSION}/${ASSET}" "${TMPDIR_LOCALAI}/${BIN}"; then
	die "could not download ${DL}/${VERSION}/${ASSET}" \
		"Check the release exists and lists that file: https://github.com/${REPO}/releases/tag/${VERSION}"
fi

if [ ! -s "${TMPDIR_LOCALAI}/${BIN}" ]; then
	die "the downloaded file is empty" \
		"Try again, or download it by hand from https://github.com/${REPO}/releases/tag/${VERSION}"
fi

if [ -n "$EXPECTED" ]; then
	if ACTUAL="$(sha256_of "${TMPDIR_LOCALAI}/${BIN}")"; then
		if [ "$ACTUAL" != "$EXPECTED" ]; then
			die "checksum mismatch for ${ASSET}" \
				"expected ${EXPECTED}, got ${ACTUAL}. The download was corrupted or tampered with, so nothing was installed."
		fi
		step "checksum      ok"
	else
		step "checksum      skipped, no sha256sum, shasum or openssl on this machine"
	fi
else
	step "checksum      skipped, this release publishes no checksum file"
fi

chmod 0755 "${TMPDIR_LOCALAI}/${BIN}"

# Write to a sibling name and rename, so a running local-ai is never replaced
# underneath itself and a failed copy cannot leave a half-written binary.
if ! $SUDO cp "${TMPDIR_LOCALAI}/${BIN}" "${TARGET_DIR}/${BIN}.new"; then
	die "could not write to ${TARGET_DIR}" \
		"Re-run with INSTALL_DIR set to somewhere you can write, for example: INSTALL_DIR=\$HOME/.local/bin sh install.sh"
fi
$SUDO chmod 0755 "${TARGET_DIR}/${BIN}.new"
$SUDO mv "${TARGET_DIR}/${BIN}.new" "${TARGET_DIR}/${BIN}"

step "installed     ${TARGET_DIR}/${BIN}"

say ""
say "Next steps"
if ! on_path "$TARGET_DIR"; then
	say "  ${TARGET_DIR} is not on your PATH yet. Add it:"
	say "    export PATH=\"${TARGET_DIR}:\$PATH\""
fi
say "  Start the API and pull a model in one go:"
say "    ${BIN} run qwen3.5-35b-a3b-apex"
say "  Or just start the server and use the web interface:"
say "    ${BIN}"
say "    open http://localhost:8080"
say ""
say "  Backends download themselves the first time a model asks for one."
say "  Documentation: https://localai.io/docs/"
