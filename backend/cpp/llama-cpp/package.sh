#!/bin/bash

# Script to copy the appropriate libraries based on architecture
# This script is used in the final stage of the Dockerfile

set -e

CURDIR=$(dirname "$(realpath $0)")
REPO_ROOT="${CURDIR}/../../.."

# Create lib directory
mkdir -p $CURDIR/package/lib

cp -avrf $CURDIR/llama-cpp-* $CURDIR/package/
cp -rfv $CURDIR/run.sh $CURDIR/package/

# Bundle the ggml shared backends produced by the CPU_ALL_VARIANTS build (libggml-base.so,
# libggml.so, libllama.so and the per-microarch libggml-cpu-*.so), all into package/lib.
#
# Two distinct resolution mechanisms both land here:
#   - NEEDED deps (libggml-base/libggml/libllama): resolved by the dynamic linker via the
#     LD_LIBRARY_PATH=$CURDIR/lib that run.sh exports.
#   - The per-microarch libggml-cpu-*.so are NOT linked; ggml *discovers* them at runtime by
#     scanning the executable's own directory (readlink /proc/self/exe). run.sh launches via
#     the bundled $CURDIR/lib/ld.so, so /proc/self/exe -> .../lib/ld.so and ggml scans lib/.
#     That is why the variants must sit in lib/ (next to ld.so), not just on the link path.
# No-op on builds (arm64/darwin) that don't produce the all-variants set.
if [ -d "$CURDIR/ggml-shared-libs" ]; then
    echo "Bundling ggml shared backends (CPU_ALL_VARIANTS)..."
    cp -avf $CURDIR/ggml-shared-libs/*.so* $CURDIR/package/lib/
fi

# Detect architecture and copy appropriate libraries
source "$CURDIR/../../../scripts/build/package-system-libs.sh" "$CURDIR/package/lib" ""

# Package GPU libraries based on BUILD_TYPE
# The GPU library packaging script will detect BUILD_TYPE and copy appropriate GPU libraries
GPU_LIB_SCRIPT="${REPO_ROOT}/scripts/build/package-gpu-libs.sh"
if [ -f "$GPU_LIB_SCRIPT" ]; then
    echo "Packaging GPU libraries for BUILD_TYPE=${BUILD_TYPE:-cpu}..."
    source "$GPU_LIB_SCRIPT" "$CURDIR/package/lib"
    package_gpu_libs
fi

echo "Packaging completed successfully" 
ls -liah $CURDIR/package/
ls -liah $CURDIR/package/lib/