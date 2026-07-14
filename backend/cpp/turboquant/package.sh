#!/bin/bash

# Script to copy the appropriate libraries based on architecture
# This script is used in the final stage of the Dockerfile

set -e

CURDIR=$(dirname "$(realpath $0)")
REPO_ROOT="${CURDIR}/../../.."

# Create lib directory
mkdir -p $CURDIR/package/lib

cp -avrf $CURDIR/turboquant-* $CURDIR/package/
cp -rfv $CURDIR/run.sh $CURDIR/package/

# Bundle the ggml shared backends from the CPU_ALL_VARIANTS build into package/lib. ggml
# discovers the per-microarch libggml-cpu-*.so by scanning the executable directory, which
# (via the bundled lib/ld.so that run.sh launches through) resolves to lib/. See the
# matching comment in backend/cpp/llama-cpp/package.sh. No-op on the fallback/ROCm builds.
if [ -d "$CURDIR/ggml-shared-libs" ]; then
    echo "Bundling ggml shared backends (CPU_ALL_VARIANTS)..."
    cp -avf $CURDIR/ggml-shared-libs/*.so* $CURDIR/package/lib/
fi

# Detect architecture and copy appropriate libraries
if [ -f "/lib64/ld-linux-x86-64.so.2" ]; then
    # x86_64 architecture
    echo "Detected x86_64 architecture, copying x86_64 libraries..."
    cp -arfLv /lib64/ld-linux-x86-64.so.2 $CURDIR/package/lib/ld.so
    cp -arfLv /lib/x86_64-linux-gnu/libc.so.6 $CURDIR/package/lib/libc.so.6
    cp -arfLv /lib/x86_64-linux-gnu/libgcc_s.so.1 $CURDIR/package/lib/libgcc_s.so.1
    cp -arfLv /lib/x86_64-linux-gnu/libstdc++.so.6 $CURDIR/package/lib/libstdc++.so.6
    cp -arfLv /lib/x86_64-linux-gnu/libm.so.6 $CURDIR/package/lib/libm.so.6
    cp -arfLv /lib/x86_64-linux-gnu/libgomp.so.1 $CURDIR/package/lib/libgomp.so.1
    cp -arfLv /lib/x86_64-linux-gnu/libdl.so.2 $CURDIR/package/lib/libdl.so.2
    cp -arfLv /lib/x86_64-linux-gnu/librt.so.1 $CURDIR/package/lib/librt.so.1
    cp -arfLv /lib/x86_64-linux-gnu/libpthread.so.0 $CURDIR/package/lib/libpthread.so.0
elif [ -f "/lib/ld-linux-aarch64.so.1" ]; then
    # ARM64 architecture
    echo "Detected ARM64 architecture, copying ARM64 libraries..."
    cp -arfLv /lib/ld-linux-aarch64.so.1 $CURDIR/package/lib/ld.so
    cp -arfLv /lib/aarch64-linux-gnu/libc.so.6 $CURDIR/package/lib/libc.so.6
    cp -arfLv /lib/aarch64-linux-gnu/libgcc_s.so.1 $CURDIR/package/lib/libgcc_s.so.1
    cp -arfLv /lib/aarch64-linux-gnu/libstdc++.so.6 $CURDIR/package/lib/libstdc++.so.6
    cp -arfLv /lib/aarch64-linux-gnu/libm.so.6 $CURDIR/package/lib/libm.so.6
    cp -arfLv /lib/aarch64-linux-gnu/libgomp.so.1 $CURDIR/package/lib/libgomp.so.1
    cp -arfLv /lib/aarch64-linux-gnu/libdl.so.2 $CURDIR/package/lib/libdl.so.2
    cp -arfLv /lib/aarch64-linux-gnu/librt.so.1 $CURDIR/package/lib/librt.so.1
    cp -arfLv /lib/aarch64-linux-gnu/libpthread.so.0 $CURDIR/package/lib/libpthread.so.0
else
    echo "Error: Could not detect architecture"
    exit 1
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
