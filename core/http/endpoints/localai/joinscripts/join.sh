#!/bin/sh
# LocalAI worker join script. It installs the local-ai binary and a service that
# runs "local-ai worker", and joins the cluster of the frontend that served it.
#
#   curl -fsSL 'https://ai.example.com/join.sh?t=<join token>' | sudo sh
#
# The join token is in this file. Do not share it or paste it in a ticket. It
# works for a short time, a limited number of times, and the frontend can revoke
# it. The script never prints it and never puts it on a command line.
#
# The service settings go to /etc/localai/worker.env, which only root can read.
# To remove the worker later, see the note at the end of the install.
set -eu

LOCALAI_REGISTER_TO=@@REGISTER_TO@@
LOCALAI_JOIN_TOKEN=@@JOIN_TOKEN@@
LOCALAI_WORKER_ROUTES=@@ROUTES@@
export LOCALAI_REGISTER_TO LOCALAI_JOIN_TOKEN LOCALAI_WORKER_ROUTES

INSTALL_URL="${LOCALAI_INSTALL_URL:-https://localai.io/install.sh}"

die() {
	printf 'localai join: %s\n' "$1" >&2
	exit 1
}

if [ "$(id -u)" != "0" ]; then
	die "run this as root, for example: curl -fsSL '<this URL>' | sudo sh"
fi

TMP="$(mktemp -d)" || die "could not create a temporary directory"
trap 'rm -rf "$TMP"' EXIT INT TERM HUP

# The installer checks the checksum of the binary it downloads.
if command -v curl >/dev/null 2>&1; then
	curl -fsSL --retry 3 -o "$TMP/install.sh" "$INSTALL_URL" || die "could not download the installer from ${INSTALL_URL}"
elif command -v wget >/dev/null 2>&1; then
	wget -q --tries=3 -O "$TMP/install.sh" "$INSTALL_URL" || die "could not download the installer from ${INSTALL_URL}"
else
	die "neither curl nor wget is installed"
fi

sh "$TMP/install.sh" --worker
