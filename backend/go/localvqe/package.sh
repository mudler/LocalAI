#!/bin/bash

# Bundle the localvqe binary, the upstream liblocalvqe.so + the per-CPU
# libggml-*.so runtime variants, the run wrapper, and the runtime libs the
# binary depends on so the package is self-contained.

set -e

CURDIR=$(dirname "$(realpath $0)")
REPO_ROOT="${CURDIR}/../../.."

mkdir -p $CURDIR/package/lib

cp -avf $CURDIR/localvqe $CURDIR/package/
# liblocalvqe.so* (with SOVERSION symlinks) and the libggml-*.so runtime
# variants — LocalVQE picks the matching CPU variant at load time.
cp -P $CURDIR/liblocalvqe.so* $CURDIR/package/ 2>/dev/null || true
cp -P $CURDIR/liblocalvqe.dylib $CURDIR/package/ 2>/dev/null || true
cp -P $CURDIR/libggml*.so* $CURDIR/package/ 2>/dev/null || true
cp -P $CURDIR/libggml*.dylib $CURDIR/package/ 2>/dev/null || true
cp -fv $CURDIR/run.sh $CURDIR/package/

# Detect architecture and copy appropriate libraries
source "$CURDIR/../../../scripts/build/package-system-libs.sh" "$CURDIR/package/lib" ""

# Package GPU libraries based on BUILD_TYPE
GPU_LIB_SCRIPT="${REPO_ROOT}/scripts/build/package-gpu-libs.sh"
if [ -f "$GPU_LIB_SCRIPT" ]; then
    echo "Packaging GPU libraries for BUILD_TYPE=${BUILD_TYPE:-cpu}..."
    source "$GPU_LIB_SCRIPT" "$CURDIR/package/lib"
    package_gpu_libs
fi

echo "Packaging completed successfully"
ls -liah $CURDIR/package/
ls -liah $CURDIR/package/lib/
