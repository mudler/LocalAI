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
#   4. Every package of the engine has a matching Hy4-Flash and Inkling package
#      pin, with the file name upstream publishes, and the bump script reads all
#      three families.
set -euo pipefail

REPO_ROOT=$(dirname "$(dirname "$(dirname "$(realpath "$0")")")")
DIR="$REPO_ROOT/backend/go/strata"
MATRIX="$REPO_ROOT/.github/backend-matrix.yml"

failures=0
fail() {
    echo "FAIL: $*"
    failures=$((failures + 1))
}

# The version a family pins for a package, read from the Makefile, so a Strata
# bump does not break the file-name checks below.
pin_version() {
    sed -n "s/^STRATA_$1_VERSION_$2?=//p" "$DIR/Makefile"
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

# Hy4-Flash: one pin set per package, named like upstream's files.
for pkg in $(packages_for cublas 12 x86_64) $(packages_for cublas 13 x86_64) $(packages_for cublas 13 aarch64) \
    $(packages_for hipblas "" x86_64) $(packages_for intel "" x86_64) $(make -C "$DIR" -s print-packages UNAME_S=Darwin UNAME_M=arm64); do
    for var in TAG VERSION SHA256; do
        grep -q "^STRATA_HY4_${var}_${pkg}?=" "$DIR/Makefile" || fail "no STRATA_HY4_${var}_${pkg} pin"
    done
done
hy4_version=$(pin_version HY4 linux_x86_64_cuda_cuda12)
[ -n "$hy4_version" ] || fail "no STRATA_HY4_VERSION_linux_x86_64_cuda_cuda12 pin"
want="strata-hy4-${hy4_version}-linux-x86_64-cuda-cuda12.tar.gz"
got=$(make -C "$DIR" -s print-asset-hy4 PKG=linux_x86_64_cuda_cuda12)
[ "$got" = "$want" ] || fail "Hy4 asset name: want '$want', got '$got'"
grep -q '"HY4": "strata-hy4"' "$REPO_ROOT/.github/bump_strata.sh" || fail "bump_strata.sh does not read the Hy4 packages"
# `make engine` fetches the Hy4 packages into engines-hy4/ with the program strata-hy4.
if ! make -C "$DIR" -n engine BUILD_TYPE=cublas CUDA_MAJOR_VERSION=12 UNAME_S=Linux UNAME_M=x86_64 | grep -q "strata-hy4-${hy4_version}-linux-x86_64-cuda-cuda12.tar.gz.*engines-hy4 strata-hy4"; then
    fail "make engine does not fetch the Hy4 package into engines-hy4"
fi

# Inkling 2 Mini: the same, with its own program strata-inkling.
for pkg in $(packages_for cublas 12 x86_64) $(packages_for cublas 13 x86_64) $(packages_for cublas 13 aarch64) \
    $(packages_for hipblas "" x86_64) $(packages_for intel "" x86_64) $(make -C "$DIR" -s print-packages UNAME_S=Darwin UNAME_M=arm64); do
    for var in TAG VERSION SHA256; do
        grep -q "^STRATA_INKLING_${var}_${pkg}?=" "$DIR/Makefile" || fail "no STRATA_INKLING_${var}_${pkg} pin"
    done
done
inkling_version=$(pin_version INKLING linux_x86_64_cuda_cuda12)
[ -n "$inkling_version" ] || fail "no STRATA_INKLING_VERSION_linux_x86_64_cuda_cuda12 pin"
want="strata-inkling-${inkling_version}-linux-x86_64-cuda-cuda12.tar.gz"
got=$(make -C "$DIR" -s print-asset-inkling PKG=linux_x86_64_cuda_cuda12)
[ "$got" = "$want" ] || fail "Inkling asset name: want '$want', got '$got'"
grep -q '"INKLING": "strata-inkling"' "$REPO_ROOT/.github/bump_strata.sh" || fail "bump_strata.sh does not read the Inkling packages"
if ! make -C "$DIR" -n engine BUILD_TYPE=cublas CUDA_MAJOR_VERSION=12 UNAME_S=Linux UNAME_M=x86_64 | grep -q "strata-inkling-${inkling_version}-linux-x86_64-cuda-cuda12.tar.gz.*engines-inkling strata-inkling"; then
    fail "make engine does not fetch the Inkling package into engines-inkling"
fi
# The Python sources of the Inkling family are fetched with the others.
grep -q 'inkling_tokenizer.py' "$DIR/Makefile" && grep -q 'inkling_dialect.py' "$DIR/Makefile" || fail "make engine-source does not fetch the Inkling Python sources"

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
