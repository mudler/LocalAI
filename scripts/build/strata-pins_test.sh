#!/bin/bash
# Guards the strata backend packaging, offline.
#
# backend/go/strata/Makefile pins a Strata release and fetches a prebuilt engine
# package per target. Three things have to stay true, and none of them shows up
# until a release image is built:
#
#   1. Every pin is well formed (40-hex commit, 64-hex SHA-256 values).
#   2. Every build the CI matrix asks for maps to an engine package, and every
#      package the Makefile knows is used by some matrix entry. A matrix entry
#      with no package would build an image that cannot start.
#   3. A target Strata does not support (cpu, vulkan, Jetson) is refused with a
#      message, not built into an empty image.
set -euo pipefail

REPO_ROOT=$(dirname "$(dirname "$(dirname "$(realpath "$0")")")")
DIR="$REPO_ROOT/backend/go/strata"
MATRIX="$REPO_ROOT/.github/backend-matrix.yml"

failures=0
fail() {
    echo "FAIL: $*"
    failures=$((failures + 1))
}

if ! make -C "$DIR" -s check-pins >/dev/null 2>&1; then
    fail "make check-pins reports a malformed pin"
fi

# What the Makefile would ship for a build: BUILD_TYPE, CUDA major, uname -m.
packages_for() {
    make -C "$DIR" -s print-packages BUILD_TYPE="$1" CUDA_MAJOR_VERSION="$2" UNAME_M="$3" UNAME_S=Linux
}

expect() {
    local want="$1" got
    shift
    got=$(packages_for "$@")
    [ "$got" = "$want" ] || fail "BUILD_TYPE=$1 CUDA=$2 arch=$3: want '$want', got '$got'"
}

expect "linux_x86_64_cuda_cuda12" cublas 12 x86_64
expect "linux_x86_64_cuda_cuda13" cublas 13 x86_64
expect "linux_aarch64_cuda_cuda13" cublas 13 aarch64
expect "linux_x86_64_hip_rx9070 linux_x86_64_hip_rx7900 linux_x86_64_hip_rx6000 linux_x86_64_hip_halo" hipblas "" x86_64
expect "linux_x86_64_sycl" intel "" x86_64
# Not supported by Strata: no package, so `make engine` must refuse.
expect "" "" "" x86_64
expect "" vulkan "" x86_64
expect "" cublas 12 aarch64
expect "" hipblas "" aarch64
expect "" intel "" aarch64

if make -C "$DIR" -s engine-packages BUILD_TYPE=vulkan UNAME_S=Linux UNAME_M=x86_64 >/dev/null 2>&1; then
    fail "make engine-packages accepted BUILD_TYPE=vulkan"
fi

# Every strata matrix entry must resolve to a package.
entries=$(python3 -I - "$MATRIX" <<'PY'
import sys, yaml
m = yaml.safe_load(open(sys.argv[1]))
for e in m["include"]:
    if e["backend"] == "strata":
        arch = "aarch64" if e["platforms"] == "linux/arm64" else "x86_64"
        print(e["build-type"], e["cuda-major-version"] or "-", arch, e["tag-suffix"])
PY
)
[ -n "$entries" ] || fail "no strata entries in the Linux matrix"
while read -r bt cuda arch suffix; do
    [ "$cuda" = "-" ] && cuda=""
    got=$(packages_for "$bt" "$cuda" "$arch")
    [ -n "$got" ] || fail "matrix entry $suffix (BUILD_TYPE=$bt CUDA=$cuda $arch) has no engine package"
done <<<"$entries"

if [ "$failures" -gt 0 ]; then
    echo "$failures failure(s)"
    exit 1
fi
echo "ok"
