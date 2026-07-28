#!/bin/sh
# install-fizzbee: download the pinned FizzBee release and verify its checksum.
# FizzBee is pre-1.0 and single-maintainer, so we PIN a version + sha256 rather
# than tracking latest or building from source (its primary build is Bazel).
# See formal-verification/README.md.
#
# Usage:
#   scripts/install-fizzbee.sh [dest_dir]   (default dest: ./.tools/fizzbee)
#
# First-time pinning: run once with no recorded checksum; the script prints the
# computed sha256. Record it in formal-verification/fizzbee.sha256 as
# "<sha256>  <asset>" and commit, then CI verifies it on every run.
set -eu

VERSION=${FIZZBEE_VERSION:-v0.5.2}
DEST=${1:-".tools/fizzbee"}
ROOT=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
SHA_FILE="$ROOT/formal-verification/fizzbee.sha256"

# Detect platform -> release asset name.
os=$(uname -s)
arch=$(uname -m)
case "$os" in
    Linux)  plat="linux" ;;
    Darwin) plat="macos" ;;
    *) echo "unsupported OS: $os" >&2; exit 1 ;;
esac
case "$arch" in
    x86_64|amd64) cpu="x86" ;;
    arm64|aarch64) cpu="arm" ;;
    *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac

asset="fizzbee-${VERSION}-${plat}_${cpu}.tar.gz"
url="https://github.com/fizzbee-io/fizzbee/releases/download/${VERSION}/${asset}"
inner="fizzbee-${VERSION}-${plat}_${cpu}"

# Idempotent: if the pinned version is already extracted (e.g. restored from a
# CI cache), do nothing. This keeps the install step a no-op on cache hits and
# avoids re-downloading the (large) bundle.
if [ -x "$DEST/$inner/fizz" ] && [ -L "$DEST/fizz" ]; then
    echo "==> FizzBee $VERSION already installed at $DEST/$inner"
    exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "==> downloading $url"
curl -fL -o "$tmp/$asset" "$url"

got=$(sha256sum "$tmp/$asset" | awk '{print $1}')
if [ -f "$SHA_FILE" ]; then
    want=$(awk -v a="$asset" '$2==a {print $1}' "$SHA_FILE")
    if [ -z "$want" ]; then
        echo "ERROR: no recorded sha256 for $asset in $SHA_FILE" >&2
        echo "       computed: $got  $asset" >&2
        exit 1
    fi
    if [ "$got" != "$want" ]; then
        echo "ERROR: sha256 mismatch for $asset" >&2
        echo "       want: $want" >&2
        echo "       got:  $got" >&2
        exit 1
    fi
    echo "==> checksum verified"
else
    echo "WARNING: $SHA_FILE not found -- record this line and commit it:" >&2
    echo "$got  $asset"
fi

# The tarball unpacks to a self-contained, version+platform-named directory:
#   fizzbee-<version>-<plat>_<cpu>/
#     fizz             <- the CLI wrapper (entrypoint; invoke THIS)
#     parser/parser_bin <- the .fizz frontend (bundled; no system Python needed)
#     fizzbee          <- the Go model-checker binary
#     fizz.env         <- resolves the above RELATIVE to the dir holding `fizz`
#     mbt_gen.zip      <- MBT generator (only this needs system python)
# The whole directory must stay intact (fizz.env is path-relative), so we keep
# it and publish a STABLE symlink `$DEST/fizz` -> the versioned wrapper. The
# wrapper does readlink -f on itself, so the symlink still resolves fizz.env.
mkdir -p "$DEST"
rm -rf "$DEST/$inner"
tar -xzf "$tmp/$asset" -C "$DEST"
if [ ! -x "$DEST/$inner/fizz" ]; then
    echo "ERROR: expected $DEST/$inner/fizz after extraction; tarball layout changed" >&2
    exit 1
fi
ln -sfn "$inner/fizz" "$DEST/fizz"

echo "==> installed FizzBee $VERSION to $DEST/$inner"
echo "    frontend:  $DEST/$inner/parser/parser_bin"
echo "    entrypoint: $DEST/fizz  (stable symlink -> $inner/fizz)"
echo
echo "The realtime-conformance gate auto-detects \$DEST/fizz; nothing else needed."
echo "To use it directly, run:  $DEST/fizz <spec.fizz>"
