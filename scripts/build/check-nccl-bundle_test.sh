#!/usr/bin/env bash
# Tests scripts/build/check-nccl-bundle.sh with small stub libraries: a package
# whose ELFs need libnccl.so.2 must carry it, and a builder with NCCL headers
# must produce a libggml-cuda.so that links NCCL.
#
# Requires gcc and readelf (present in the build images); skips otherwise.
set -euo pipefail

CURDIR=$(dirname "$(realpath "$0")")
CHECK="$CURDIR/check-nccl-bundle.sh"

if ! command -v gcc >/dev/null 2>&1 || ! command -v readelf >/dev/null 2>&1; then
    echo "SKIP: gcc or readelf not available"
    exit 0
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
fail() { echo "FAIL: $*"; exit 1; }

echo 'int ncclStub(void){return 0;}' > "$WORK/nccl.c"
gcc -shared -fPIC -Wl,-soname,libnccl.so.2 -o "$WORK/libnccl.so.2.26.2" "$WORK/nccl.c"
ln -s libnccl.so.2.26.2 "$WORK/libnccl.so"
echo 'int ncclStub(void); int cudaStub(void){return ncclStub();}' > "$WORK/cuda.c"
gcc -shared -fPIC -o "$WORK/libggml-cuda-nccl.so" "$WORK/cuda.c" -L"$WORK" -lnccl
echo 'int cudaStub(void){return 0;}' > "$WORK/plain.c"
gcc -shared -fPIC -o "$WORK/libggml-cuda-plain.so" "$WORK/plain.c"
touch "$WORK/nccl.h"
echo 'BSD-3-Clause notice' > "$WORK/copyright"

new_pkg() {
    local p="$WORK/pkg-$1"
    mkdir -p "$p/lib"
    echo "$p"
}

# 1. Linked and bundled: passes and ships the license.
p=$(new_pkg ok)
cp "$WORK/libggml-cuda-nccl.so" "$p/lib/libggml-cuda.so"
cp "$WORK/libnccl.so.2.26.2" "$p/lib/" && ln -s libnccl.so.2.26.2 "$p/lib/libnccl.so.2"
NCCL_COPYRIGHT="$WORK/copyright" bash "$CHECK" "$p" "$WORK/nccl.h" >/dev/null || fail "bundled package rejected"
[ -f "$p/licenses/NCCL-LICENSE.txt" ] || fail "NCCL license not copied"

# 2. Linked but not bundled: fails.
p=$(new_pkg missing)
cp "$WORK/libggml-cuda-nccl.so" "$p/lib/libggml-cuda.so"
if bash "$CHECK" "$p" "$WORK/nccl.h" >/dev/null 2>&1; then fail "package without libnccl.so.2 accepted"; fi

# 3. Builder has NCCL headers but the CUDA backend does not link NCCL: fails.
p=$(new_pkg notfound)
cp "$WORK/libggml-cuda-plain.so" "$p/lib/libggml-cuda.so"
if bash "$CHECK" "$p" "$WORK/nccl.h" >/dev/null 2>&1; then fail "CUDA backend without NCCL accepted on an NCCL builder"; fi

# 4. No NCCL in the builder (other flavors, JetPack): passes, no license.
p=$(new_pkg none)
cp "$WORK/libggml-cuda-plain.so" "$p/lib/libggml-cuda.so"
bash "$CHECK" "$p" "$WORK/no-such-header.h" >/dev/null || fail "package of a builder without NCCL rejected"
[ ! -e "$p/licenses" ] || fail "license copied for a package without NCCL"

echo "PASS: check-nccl-bundle.sh guards the NCCL bundle"
