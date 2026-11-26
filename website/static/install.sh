#!/bin/sh
# LocalAI installer.
#
#   curl -sSL https://localai.io/install.sh | sh
#
# Downloads the release binary that matches this machine, checks it against the
# checksums published with the release, and puts it on your PATH. Nothing is
# built, nothing is left behind, and running it twice is a no-op.
#
# This script does not install Docker images. To run LocalAI in a container,
# use the compose file:
#
#   curl -fsSL https://localai.io/install/docker-compose.yaml -o docker-compose.yaml
#   docker compose up -d
#
# Environment:
#   LOCALAI_VERSION   pin a release, for example v4.7.1 (default: latest)
#   INSTALL_DIR       where to put the binary (default: /usr/local/bin,
#                     falling back to ~/.local/bin when that is not writable)
#   LOCALAI_FORCE     set to 1 to reinstall even if the same build is present
#   LOCALAI_NO_SUDO   set to 1 to never escalate, use ~/.local/bin instead
#   GITHUB_TOKEN      optional, only used to avoid GitHub API rate limits
#
# Worker mode (join a LocalAI cluster):
#
#   LOCALAI_REGISTER_TO=https://ai.example.com LOCALAI_JOIN_TOKEN=ljt_... \
#     sudo -E sh install.sh --worker
#
# Installs the binary as above, then runs "local-ai worker" as a service that
# starts at boot (a systemd unit on Linux, a launchd daemon on macOS). Run it as
# root. The settings go in a file that only root can read, and the join token is
# read from the environment and never printed or put on a command line. Running
# it again is safe: the worker keeps its own credential in its state directory
# and needs the join token once.
#
#   LOCALAI_REGISTER_TO     URL of the frontend (required)
#   LOCALAI_JOIN_TOKEN      join token from the admin (required the first time)
#   LOCALAI_WORKER_ROUTES   optional: nats, websocket or both, comma separated
#   LOCALAI_WORKER_NAME     optional node name (default: the host name)
#   LOCALAI_WORKER_DIR      state directory (default: /var/lib/local-ai on Linux,
#                           /usr/local/var/local-ai on macOS)
#
# To remove a worker: see the note printed at the end of a worker install.

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

WORKER=0
for arg in "$@"; do
	case "$arg" in
		--worker) WORKER=1 ;;
		-h | --help)
			say "Usage: install.sh [--worker]"
			say "  no option   install the local-ai binary"
			say "  --worker    also set up a service that joins a cluster; see the top of this file"
			exit 0
			;;
		*) die "unknown option: ${arg}" "Use --worker to install a cluster worker, or no option to install the binary only." ;;
	esac
done

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
				"LocalAI publishes binaries for Linux and macOS. On Windows, use the PowerShell installer: irm https://localai.io/install.ps1 | iex"
			;;
	esac

	case "$uname_m" in
		x86_64 | amd64) ARCH="amd64" ;;
		aarch64 | arm64) ARCH="arm64" ;;
		*)
			die "unsupported CPU architecture: ${uname_m}" \
				"LocalAI publishes amd64 and arm64 binaries. Build from source instead: https://localai.io/docs/contributing/build-from-source/"
			;;
	esac

	if [ "$OS" = "darwin" ] && [ "$ARCH" = "amd64" ]; then
		die "there is no Intel macOS binary in the LocalAI releases" \
			"On an Intel Mac, run the container image with Docker Compose (curl -fsSL https://localai.io/install/docker-compose.yaml -o docker-compose.yaml && docker compose up -d) or build from source: https://localai.io/docs/contributing/build-from-source/"
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

# --- worker mode ------------------------------------------------------------

# Escape a value for a systemd environment file: it must stay on one line, and
# systemd reads the double-quoted form.
env_line() {
	nl='
'
	case "$2" in
		*\"* | *\\* | *\$* | *\`* | *"$nl"*) die "the value of $1 has a character that cannot go in the service settings" ;;
	esac
	printf '%s="%s"\n' "$1" "$2"
}

# Writes the settings of the worker to $1, readable by root only. The join
# token is read from the environment, never from a command line.
write_worker_env() {
	umask 077
	{
		env_line LOCALAI_REGISTER_TO "$LOCALAI_REGISTER_TO"
		if [ -n "${LOCALAI_JOIN_TOKEN:-}" ]; then
			env_line LOCALAI_JOIN_TOKEN "$LOCALAI_JOIN_TOKEN"
		fi
		if [ -n "${LOCALAI_WORKER_ROUTES:-}" ]; then
			env_line LOCALAI_WORKER_ROUTES "$LOCALAI_WORKER_ROUTES"
		fi
		if [ -n "${LOCALAI_WORKER_NAME:-}" ]; then
			env_line LOCALAI_NODE_NAME "$LOCALAI_WORKER_NAME"
		fi
		env_line LOCALAI_MODELS_PATH "${WORKER_DIR}/models"
		env_line LOCALAI_BACKENDS_PATH "${WORKER_DIR}/backends"
	} >"$1.new"
	chmod 0600 "$1.new"
	mv "$1.new" "$1"
}

setup_worker() {
	say ""
	say "Setting up the worker"

	[ "$(id -u)" = "0" ] || die "worker mode must run as root" \
		"Run it again with sudo, for example: curl -fsSL <join script URL> | sudo sh"
	[ -n "${LOCALAI_REGISTER_TO:-}" ] || die "LOCALAI_REGISTER_TO is not set" \
		"Set it to the URL of the frontend, for example https://ai.example.com"

	# The worker keeps its credential in its state directory. With no credential
	# yet, a join token is needed.
	if [ "$OS" = "darwin" ]; then
		WORKER_DIR="${LOCALAI_WORKER_DIR:-/usr/local/var/local-ai}"
	else
		WORKER_DIR="${LOCALAI_WORKER_DIR:-/var/lib/local-ai}"
	fi
	if [ -z "${LOCALAI_JOIN_TOKEN:-}" ] && [ ! -f "${WORKER_DIR}/data/node-credential.json" ]; then
		die "LOCALAI_JOIN_TOKEN is not set" \
			"Ask an admin for a join token (Nodes page, Add worker), then run this again."
	fi

	BIN_PATH="${TARGET_DIR}/${BIN}"
	mkdir -p "${WORKER_DIR}/models" "${WORKER_DIR}/backends" "${WORKER_DIR}/data"

	if [ "$OS" = "darwin" ]; then
		setup_worker_launchd
	else
		setup_worker_systemd
	fi

	say ""
	say "The worker is running and has asked the frontend to join."
	say "An admin approves it on the Nodes page of the frontend unless the token"
	say "approves it by itself."
	say ""
	say "To remove this worker later:"
	if [ "$OS" = "darwin" ]; then
		say "  sudo launchctl bootout system/io.localai.worker"
		say "  sudo rm /Library/LaunchDaemons/io.localai.worker.plist /etc/localai/worker.env"
	else
		say "  sudo systemctl disable --now localai-worker"
		say "  sudo rm /etc/systemd/system/localai-worker.service /etc/localai/worker.env"
		say "  sudo systemctl daemon-reload"
	fi
	say "  sudo rm -rf ${WORKER_DIR}      # also removes the credential, models and backends"
	say "  and delete the node on the Nodes page of the frontend."
}

setup_worker_systemd() {
	have systemctl || die "systemd is not available on this machine" \
		"Start the worker by hand: LOCALAI_REGISTER_TO=... LOCALAI_JOIN_TOKEN=... ${BIN_PATH} worker"

	# A service account, when the machine allows one. Otherwise the worker runs as root.
	RUN_USER="root"
	if have useradd || have adduser; then
		if ! id localai >/dev/null 2>&1; then
			if have useradd; then
				useradd --system --home-dir "$WORKER_DIR" --shell /usr/sbin/nologin localai 2>/dev/null || true
			else
				adduser --system --home "$WORKER_DIR" --no-create-home --shell /usr/sbin/nologin localai 2>/dev/null || true
			fi
		fi
		if id localai >/dev/null 2>&1; then
			RUN_USER="localai"
			# Access to GPUs, when the groups exist.
			for g in video render; do
				if getent group "$g" >/dev/null 2>&1; then
					usermod -aG "$g" localai 2>/dev/null || true
				fi
			done
			chown -R localai "$WORKER_DIR"
		fi
	fi
	chmod 0700 "${WORKER_DIR}/data"

	mkdir -p /etc/localai
	WORKER_ENV="/etc/localai/worker.env"
	if [ -n "${LOCALAI_JOIN_TOKEN:-}" ] || [ ! -f "$WORKER_ENV" ]; then
		write_worker_env "$WORKER_ENV"
	fi
	step "settings      ${WORKER_ENV} (root only)"

	cat >/etc/systemd/system/localai-worker.service <<UNIT
[Unit]
Description=LocalAI worker
After=network-online.target
Wants=network-online.target

[Service]
User=${RUN_USER}
EnvironmentFile=${WORKER_ENV}
ExecStart=${BIN_PATH} worker
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT
	systemctl daemon-reload
	systemctl enable localai-worker >/dev/null 2>&1
	systemctl restart localai-worker
	step "service       localai-worker (systemd, user ${RUN_USER})"
}

setup_worker_launchd() {
	mkdir -p /etc/localai
	WORKER_ENV="/etc/localai/worker.env"
	if [ -n "${LOCALAI_JOIN_TOKEN:-}" ] || [ ! -f "$WORKER_ENV" ]; then
		write_worker_env "$WORKER_ENV"
	fi
	chmod 0700 "${WORKER_DIR}/data"
	PLIST="/Library/LaunchDaemons/io.localai.worker.plist"
	# launchd has no environment file, so a small wrapper reads the settings and
	# starts the worker. The plist holds no secret.
	WRAPPER="/usr/local/libexec/localai-worker"
	mkdir -p /usr/local/libexec
	cat >"$WRAPPER" <<WRAP
#!/bin/sh
set -a
. ${WORKER_ENV}
set +a
exec ${BIN_PATH} worker
WRAP
	chmod 0755 "$WRAPPER"
	cat >"$PLIST" <<PLISTEOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>io.localai.worker</string>
  <key>ProgramArguments</key><array><string>${WRAPPER}</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/localai-worker.log</string>
  <key>StandardErrorPath</key><string>/var/log/localai-worker.log</string>
</dict>
</plist>
PLISTEOF
	chmod 0644 "$PLIST"
	launchctl bootout system/io.localai.worker >/dev/null 2>&1 || true
	launchctl bootstrap system "$PLIST"
	step "service       io.localai.worker (launchd)"
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
UPTODATE=0
if fetch_to_file "${DL}/${VERSION}/${SUMS}" "${TMPDIR_LOCALAI}/${SUMS}" 2>/dev/null; then
	EXPECTED="$(awk -v f="$ASSET" '$2 == f || $2 == "*" f {print $1}' "${TMPDIR_LOCALAI}/${SUMS}" | head -n 1)"
fi

if [ -n "$EXPECTED" ] && [ "${LOCALAI_FORCE:-0}" != "1" ] && [ -x "${TARGET_DIR}/${BIN}" ]; then
	CURRENT="$(sha256_of "${TARGET_DIR}/${BIN}" 2>/dev/null || true)"
	if [ -n "$CURRENT" ] && [ "$CURRENT" = "$EXPECTED" ]; then
		step "already at    ${VERSION}, nothing to do"
		UPTODATE=1
		if [ "$WORKER" != "1" ]; then
			say ""
			say "Start it with:  ${BIN}"
			exit 0
		fi
	fi
fi

if [ "$UPTODATE" != "1" ]; then
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
fi

if [ "$WORKER" = "1" ]; then
	setup_worker
	exit 0
fi

say ""
say "Next steps"
if ! on_path "$TARGET_DIR"; then
	say "  ${TARGET_DIR} is not on your PATH yet. Add it:"
	say "    export PATH=\"${TARGET_DIR}:\$PATH\""
fi
say "  Start the server:"
say "    ${BIN}"
say "  Then open http://localhost:8080 and install a model from there."
say "  To run a model directly, name it, for example:"
say "    ${BIN} run gemma-4-e2b-it-qat-q4_0"
say ""
say "  Backends download themselves the first time a model asks for one."
say ""
say "  To run LocalAI in a container instead, use Docker Compose:"
say "    curl -fsSL https://localai.io/install/docker-compose.yaml -o docker-compose.yaml"
say "    docker compose up -d"
say "  Documentation: https://localai.io/docs/"
