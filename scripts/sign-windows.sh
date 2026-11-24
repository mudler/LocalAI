#!/bin/sh
# Authenticode-signs a Windows executable with osslsigncode.
#
#   scripts/sign-windows.sh <goos> <path-to-exe>
#
# Called by GoReleaser as a post-build hook (.goreleaser.yaml), before the
# archive and checksum steps, so the published checksum covers the signed file.
#
# It does nothing (exit 0) unless the target is Windows and a certificate is
# configured, so forks, pull requests and snapshot builds stay unsigned and
# green. When a certificate is configured, any failure stops the release: a
# half-configured signing setup must not publish an unsigned file by accident.
#
# Environment:
#   WINDOWS_SIGN_PFX         base64 of the PFX (PKCS#12) file with the code
#                            signing certificate and its private key
#   WINDOWS_SIGN_PASSWORD    password of the PFX (empty when it has none)
#   WINDOWS_SIGN_TIMESTAMP_URL  RFC 3161 timestamp server
#                            (default: http://timestamp.digicert.com)

set -eu

goos="${1:-}"
exe="${2:-}"

if [ "$goos" != "windows" ]; then
	exit 0
fi

if [ -z "${WINDOWS_SIGN_PFX:-}" ]; then
	echo "sign-windows: WINDOWS_SIGN_PFX is not set, leaving ${exe} unsigned"
	exit 0
fi

if [ -z "$exe" ] || [ ! -f "$exe" ]; then
	echo "sign-windows: no such file: ${exe}" >&2
	exit 1
fi

if ! command -v osslsigncode >/dev/null 2>&1; then
	echo "sign-windows: osslsigncode is not installed" >&2
	exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT INT TERM HUP
umask 077

printf '%s' "$WINDOWS_SIGN_PFX" | base64 -d >"$work/cert.pfx"
printf '%s' "${WINDOWS_SIGN_PASSWORD:-}" >"$work/pass"

osslsigncode sign \
	-pkcs12 "$work/cert.pfx" \
	-readpass "$work/pass" \
	-h sha256 \
	-n "LocalAI" \
	-i "https://localai.io" \
	-ts "${WINDOWS_SIGN_TIMESTAMP_URL:-http://timestamp.digicert.com}" \
	-in "$exe" \
	-out "$work/signed.exe"

if [ ! -s "$work/signed.exe" ]; then
	echo "sign-windows: osslsigncode produced no output" >&2
	exit 1
fi

cp "$work/signed.exe" "$exe"
echo "sign-windows: signed ${exe}"
