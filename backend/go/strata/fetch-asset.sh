#!/bin/sh
# Download a release asset and check its SHA-256 before anything uses it.
# Usage: fetch-asset.sh <url> <sha256> <output file>
# A file that fails the check is deleted and the script exits non-zero.
set -eu

url=${1:?url}
want=${2:?sha256}
out=${3:?output file}

mkdir -p "$(dirname "$out")"
curl -fL --retry 5 --retry-delay 5 --retry-connrefused -sS -o "$out" "$url"

if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$out" | cut -d' ' -f1)
else
	got=$(shasum -a 256 "$out" | cut -d' ' -f1)
fi

if [ "$got" != "$want" ]; then
	rm -f "$out"
	echo "strata: SHA-256 mismatch for $url" >&2
	echo "strata:   expected $want" >&2
	echo "strata:   got      $got" >&2
	exit 1
fi
echo "==> sha256 ok: $(basename "$out")"
