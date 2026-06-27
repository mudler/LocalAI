#!/bin/bash

set -ex

# Darwin/Metal build for the llama-cpp-localai-paged backend. Mirrors
# scripts/build/llama-cpp-darwin.sh exactly, swapping the build dir, binary names,
# shared-lib dir and output tar for the paged wrapper. The paged wrapper Makefile
# (backend/cpp/llama-cpp-localai-paged) reuses backend/cpp/llama-cpp's CMakeLists
# /grpc-server and applies its own vendored paged patch series (patches/paged/)
# onto the cloned tree, so the Darwin/Metal path is identical: ggml
# CPU_ALL_VARIANTS + GGML_METAL=ON, and --target ggml pulls in ggml-metal via
# add_dependencies so the Metal GPU backend is produced as a loadable
# libggml-metal.dylib. The new paged GDN/conv ops have no Metal kernel, so a
# gated-DeltaNet (qwen35) model falls back to the CPU reference op at runtime
# (assert/fall-back is made SAFE by the fused-op backend gate, patch 0030); a
# non-qwen35 model gets the full paged-KV path on Metal.

IMAGE_NAME="${IMAGE_NAME:-localai/llama-cpp-localai-paged-darwin}"

pushd backend/cpp/llama-cpp-localai-paged

# Single build via ggml CPU_ALL_VARIANTS: one binary plus the per-microarch Apple/arm
# dylibs (apple_m1/m2_m3/m4, armv8.x) that ggml selects at runtime. GGML_METAL stays ON
# and --target ggml also builds ggml-metal (via add_dependencies), so the Metal GPU
# backend is still produced as a loadable libggml-metal.dylib.
make llama-cpp-localai-paged-cpu-all && \
make llama-cpp-localai-paged-grpc && \
make llama-cpp-localai-paged-rpc-server

popd

mkdir -p build/darwin
mkdir -p backend-images
mkdir -p build/darwin/lib

cp -rf backend/cpp/llama-cpp-localai-paged/llama-cpp-localai-paged-cpu-all build/darwin/
cp -rf backend/cpp/llama-cpp-localai-paged/llama-cpp-localai-paged-grpc build/darwin/
cp -rf backend/cpp/llama-cpp-localai-paged/llama-cpp-localai-paged-rpc-server build/darwin/

# Distribute the shared ggml/llama libraries from the CPU_ALL_VARIANTS build. Unlike the
# old fully-static fallback build, these have @rpath install names, so the otool loop below
# (which only copies deps that exist on disk) will not pick them up. The split is by suffix:
#  - ggml emits its loadable backends (per-microarch CPU variants, metal, blas) with a .so
#    suffix EVEN ON DARWIN. These go in the package ROOT next to the binary, because darwin
#    run.sh execs the binary directly (no bundled ld.so) so ggml's executable-directory
#    scan looks there.
#  - the core libraries (libggml-base/libggml/libllama/libllama-common/libmtmd) use the
#    platform .dylib suffix and are NEEDED deps; they go in lib/, resolved at load time via
#    the DYLD_LIBRARY_PATH=lib that run.sh exports. -a preserves the version symlinks.
SHLIBS=backend/cpp/llama-cpp-localai-paged/ggml-shared-libs
cp -a $SHLIBS/*.so build/darwin/
cp -a $SHLIBS/*.dylib build/darwin/lib/

# Set default additional libs only for Darwin on M chips (arm64)
if [[ "$(uname -s)" == "Darwin" && "$(uname -m)" == "arm64" ]]; then
    ADDITIONAL_LIBS=${ADDITIONAL_LIBS:-$(ls /opt/homebrew/Cellar/protobuf/**/lib/libutf8_validity*.dylib 2>/dev/null)}
else
    ADDITIONAL_LIBS=${ADDITIONAL_LIBS:-""}
fi

for file in $ADDITIONAL_LIBS; do
  cp -rfv $file build/darwin/lib
done

for file in build/darwin/*; do
  LIBS="$(otool -L $file | awk 'NR > 1 { system("echo " $1) } ' | xargs echo)"
  for lib in $LIBS; do
    # only libraries ending in dylib
    if [[ "$lib" == *.dylib ]]; then
        if [ -e "$lib" ]; then
            cp -rvf "$lib" build/darwin/lib
        fi
    fi
  done
done

echo "--------------------------------"
echo "ADDITIONAL_LIBS: $ADDITIONAL_LIBS"
echo "--------------------------------"

echo "Bundled libraries:"
ls -la build/darwin/lib


cp -rf backend/cpp/llama-cpp-localai-paged/run.sh build/darwin/

PLATFORMARCH="${PLATFORMARCH:-darwin/arm64}"

./local-ai util create-oci-image \
        build/darwin/. \
        --output ./backend-images/llama-cpp-localai-paged.tar \
        --image-name $IMAGE_NAME \
        --platform $PLATFORMARCH

rm -rf build/darwin
