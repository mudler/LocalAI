#!/bin/sh
# Fetches the runtime files of the Sandbox add-on package for one platform:
# runsc and its gvisor-bin folder, from the gVisor release that
# package/runsc.json pins. It checks the checksum the release publishes for
# the tarball and the checksum pinned for runsc, and fails on either.
#
#   fetch-runtime.sh <os/arch> <output-dir>
set -eu
[ $# -eq 2 ] || { echo "usage: fetch-runtime.sh <os/arch> <output-dir>" >&2; exit 2; }
platform=$1
out=$2
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
read_pin() { python3 -I - "$here/runsc.json" "$platform" "$1" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
p = d["platforms"].get(sys.argv[2])
if p is None:
    sys.exit("fetch-runtime.sh: no gVisor release is pinned for " + sys.argv[2])
print(p[sys.argv[3]])
PY
}
url=$(read_pin url)
sha512=$(read_pin sha512)
sha256=$(read_pin runscSha256)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
curl -fsSLo "$tmp/gvisor.tar.zstd" "$url"
echo "$sha512  $tmp/gvisor.tar.zstd" | sha512sum -c -
mkdir -p "$out"
zstd -dc "$tmp/gvisor.tar.zstd" | tar -x -C "$out"
echo "$sha256  $out/runsc" | sha256sum -c -
