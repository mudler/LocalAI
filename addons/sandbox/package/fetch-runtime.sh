#!/bin/sh
# Fetches the runtime files of the Sandbox add-on package for one platform:
# runsc and its gvisor-bin folder, from the gVisor release that
# package/runsc.json pins, and the KVM host programs (cloud-hypervisor, and
# virtiofsd where upstream publishes one) that package/kvm.json pins. It checks
# the checksum the release publishes for the tarball and the checksum pinned
# for runsc, and the pinned checksums of the KVM programs, and fails on any.
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

# KVM host programs. read_kvm prints one field of a pin; an empty answer means
# upstream publishes no such build for the platform.
read_kvm() { python3 -I - "$here/kvm.json" "$platform" "$1" "$2" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
p = d[sys.argv[3]]["platforms"].get(sys.argv[2])
print("" if p is None else p.get(sys.argv[4], ""))
PY
}
url=$(read_kvm cloud-hypervisor url)
[ -n "$url" ] || { echo "fetch-runtime.sh: no Cloud Hypervisor is pinned for $platform" >&2; exit 1; }
curl -fsSLo "$out/cloud-hypervisor" "$url"
echo "$(read_kvm cloud-hypervisor sha256)  $out/cloud-hypervisor" | sha256sum -c -
chmod 0755 "$out/cloud-hypervisor"
url=$(read_kvm virtiofsd url)
if [ -n "$url" ]; then
    curl -fsSLo "$tmp/virtiofsd.zip" "$url"
    echo "$(read_kvm virtiofsd archiveSha256)  $tmp/virtiofsd.zip" | sha256sum -c -
    python3 -I - "$tmp/virtiofsd.zip" "$(read_kvm virtiofsd member)" "$out/virtiofsd" <<'PY'
import sys, zipfile
open(sys.argv[3], "wb").write(zipfile.ZipFile(sys.argv[1]).read(sys.argv[2]))
PY
    echo "$(read_kvm virtiofsd sha256)  $out/virtiofsd" | sha256sum -c -
    chmod 0755 "$out/virtiofsd"
fi
# The tool that prints the AppArmor profile of runsc (Ubuntu 24.04 and later
# need it loaded once). It is pure Go: build it for the target platform.
root=$(CDPATH= cd -- "$here/../../.." && pwd)
(cd "$root" && GOOS=${platform%/*} GOARCH=${platform#*/} CGO_ENABLED=0 go build -trimpath -o "$out/gvisor-profile" ./addons/sandbox/internal/sandbox/gvisor/cmd/gvisor-profile)
