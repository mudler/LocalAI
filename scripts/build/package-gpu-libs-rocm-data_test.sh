#!/bin/bash
# Regression test for scripts/build/package-gpu-libs.sh ROCm data bundling.
#
# Guards issue #10660: hipBLASLt (rocblaslt) resolves its TensileLibrary_lazy_gfx*.dat
# kernel data relative to the bundled libhipblaslt.so. The packager copied the
# rocblas/ data dir but not the hipblaslt/ data dir, so the bundled backend
# fell back to slow generic kernels and logged
#   rocblaslt error: Cannot read "TensileLibrary_lazy_gfx1201.dat": No such file or directory
#
# This test fabricates a fake ROCm tree containing both rocblas/ and hipblaslt/
# tensile data, points the packager at it via ROCM_BASE_DIRS, and asserts BOTH
# data directories are bundled into the target lib dir.
set -euo pipefail

CURDIR=$(dirname "$(realpath "$0")")
SCRIPT="$CURDIR/package-gpu-libs.sh"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# Fabricate a fake ROCm install with both rocblas and hipblaslt tensile data.
FAKE_ROCM="$WORK/opt/rocm"
mkdir -p "$FAKE_ROCM/lib/rocblas/library"
mkdir -p "$FAKE_ROCM/lib/hipblaslt/library"
echo "fake rocblas tensile" > "$FAKE_ROCM/lib/rocblas/library/TensileLibrary_lazy_gfx1201.dat"
echo "fake hipblaslt tensile" > "$FAKE_ROCM/lib/hipblaslt/library/TensileLibrary_lazy_gfx1201.dat"

TARGET="$WORK/target"
mkdir -p "$TARGET"

# shellcheck source=/dev/null
source "$SCRIPT" "$TARGET"

# Point the data-dir copy at the fabricated tree instead of the real /opt/rocm,
# then run the actual ROCm packager. This asserts package_rocm_libs itself
# bundles BOTH data dirs, not just that the helper works in isolation.
export BUILD_TYPE=hipblas
export ROCM_BASE_DIRS="$FAKE_ROCM"
package_rocm_libs

fail=false
if [ ! -e "$TARGET/rocblas/library/TensileLibrary_lazy_gfx1201.dat" ]; then
    echo "FAIL: rocblas tensile data was NOT bundled"
    fail=true
fi
if [ ! -e "$TARGET/hipblaslt/library/TensileLibrary_lazy_gfx1201.dat" ]; then
    echo "FAIL: hipblaslt tensile data was NOT bundled (regression of #10660)"
    fail=true
fi

if [ "$fail" = true ]; then
    ls -R "$TARGET" || true
    exit 1
fi

# ROCm 10.x layout: the files live under /opt/rocm/core-<ver>/lib and
# /opt/rocm/lib is a link to that directory, with per-arch subdirectories
# under library/. The packager must follow the link and keep the subdirectory.
FAKE_ROCM10="$WORK/opt10/rocm"
mkdir -p "$FAKE_ROCM10/core-10.1/lib/rocblas/library/gfx1151"
mkdir -p "$FAKE_ROCM10/core-10.1/lib/hipblaslt/library/gfx1151"
echo "fake rocblas tensile" > "$FAKE_ROCM10/core-10.1/lib/rocblas/library/gfx1151/TensileLibrary_lazy_gfx1151.dat"
echo "fake hipblaslt tensile" > "$FAKE_ROCM10/core-10.1/lib/hipblaslt/library/gfx1151/TensileLibrary_lazy_gfx1151.dat"
ln -s core-10.1/lib "$FAKE_ROCM10/lib"

TARGET10="$WORK/target10"
mkdir -p "$TARGET10"
TARGET_LIB_DIR="$TARGET10"
export ROCM_BASE_DIRS="$FAKE_ROCM10"
package_rocm_libs

for lib in rocblas hipblaslt; do
    if [ ! -f "$TARGET10/$lib/library/gfx1151/TensileLibrary_lazy_gfx1151.dat" ]; then
        echo "FAIL: $lib tensile data was NOT bundled from a ROCm 10.x layout (/opt/rocm/lib -> core-10.1/lib)"
        ls -R "$TARGET10" || true
        exit 1
    fi
done

echo "PASS: rocblas and hipblaslt tensile data were both bundled (ROCm 7.x and 10.x layouts)"
exit 0
