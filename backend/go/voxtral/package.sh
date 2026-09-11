#!/bin/bash

# Script to copy the appropriate libraries based on architecture

set -e

CURDIR=$(dirname "$(realpath $0)")
REPO_ROOT="${CURDIR}/../../.."

# Create lib directory
mkdir -p $CURDIR/package/lib

cp -avf $CURDIR/voxtral $CURDIR/package/
cp -fv $CURDIR/libgovoxtral-*.so $CURDIR/package/ 2>/dev/null || true
cp -fv $CURDIR/libgovoxtral-*.dylib $CURDIR/package/ 2>/dev/null || true
cp -fv $CURDIR/run.sh $CURDIR/package/

# Detect architecture and copy appropriate libraries
source "$CURDIR/../../../scripts/build/package-system-libs.sh" "$CURDIR/package/lib" ""

# OpenBLAS if available
if [ -f /usr/lib/x86_64-linux-gnu/libopenblas.so.0 ]; then
    cp -arfLv /usr/lib/x86_64-linux-gnu/libopenblas.so.0 $CURDIR/package/lib/
elif [ -f /usr/lib/aarch64-linux-gnu/libopenblas.so.0 ]; then
    cp -arfLv /usr/lib/aarch64-linux-gnu/libopenblas.so.0 $CURDIR/package/lib/
fi

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
