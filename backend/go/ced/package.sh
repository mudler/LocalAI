#!/bin/bash
#
# Bundle the ced-grpc binary, libced.so, the core runtime libs (libc/libstdc++/
# libgomp + ld.so) and the GPU runtime for the active BUILD_TYPE so the package
# is self-contained. Mirrors backend/go/parakeet-cpp/package.sh; run.sh routes
# the (CGO_ENABLED=0) binary through lib/ld.so so the packaged libc is used.

set -e

CURDIR=$(dirname "$(realpath "$0")")
REPO_ROOT="${CURDIR}/../../.."

mkdir -p "$CURDIR/package/lib"

cp -avf "$CURDIR/ced-grpc" "$CURDIR/package/"
cp -avf "$CURDIR/run.sh" "$CURDIR/package/"

cp -avf "$CURDIR"/libced.so* "$CURDIR/package/lib/" 2>/dev/null || true
cp -avf "$CURDIR"/libced.dylib "$CURDIR/package/lib/" 2>/dev/null || true
if ! ls "$CURDIR"/package/lib/libced.* >/dev/null 2>&1; then
	echo "ERROR: libced shared library not found in $CURDIR, run 'make' first" >&2
	exit 1
fi

source "$CURDIR/../../../scripts/build/package-system-libs.sh" "$CURDIR/package/lib" ""

GPU_LIB_SCRIPT="${REPO_ROOT}/scripts/build/package-gpu-libs.sh"
if [ -f "$GPU_LIB_SCRIPT" ]; then
    echo "Packaging GPU libraries for BUILD_TYPE=${BUILD_TYPE:-cpu}..."
    source "$GPU_LIB_SCRIPT" "$CURDIR/package/lib"
    package_gpu_libs
fi

echo "Packaging completed successfully"
ls -liah "$CURDIR/package/" "$CURDIR/package/lib/"
