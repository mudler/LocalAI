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
