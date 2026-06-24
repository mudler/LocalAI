#!/bin/bash

set -ex

IMAGE_NAME="${IMAGE_NAME:-localai/llama-cpp-darwin}"

pushd backend/cpp/llama-cpp

# Single build via ggml CPU_ALL_VARIANTS: one binary plus the per-microarch Apple/arm
# dylibs (apple_m1/m2_m3/m4, armv8.x) that ggml selects at runtime. GGML_METAL stays ON
# and --target ggml also builds ggml-metal (via add_dependencies), so the Metal GPU
# backend is still produced as a loadable libggml-metal.dylib.
make llama-cpp-cpu-all && \
make llama-cpp-grpc && \
make llama-cpp-rpc-server

popd

mkdir -p build/darwin
mkdir -p backend-images
mkdir -p build/darwin/lib

cp -rf backend/cpp/llama-cpp/llama-cpp-cpu-all build/darwin/
cp -rf backend/cpp/llama-cpp/llama-cpp-grpc build/darwin/
cp -rf backend/cpp/llama-cpp/llama-cpp-rpc-server build/darwin/

# Distribute the shared ggml/llama dylibs from the CPU_ALL_VARIANTS build. Unlike the old
# fully-static fallback build, these are real dylibs with @rpath install names, so the
# otool loop below (which only copies deps that exist on disk) will not pick them up.
#  - the per-microarch libggml-cpu-*.dylib go in the package ROOT, next to the binary,
#    because on darwin run.sh execs the binary directly (no bundled ld.so) and ggml
#    discovers CPU backends by scanning the executable's own directory.
#  - everything else (libggml-base/libggml/libllama/libmtmd/libggml-metal/...) goes in
#    lib/, resolved at load time via the DYLD_LIBRARY_PATH=lib that run.sh exports.
SHLIBS=backend/cpp/llama-cpp/ggml-shared-libs
cp -rfv $SHLIBS/libggml-cpu-*.dylib build/darwin/
find $SHLIBS -name '*.dylib' ! -name 'libggml-cpu-*.dylib' -exec cp -rfv {} build/darwin/lib/ \;

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


cp -rf backend/cpp/llama-cpp/run.sh build/darwin/

PLATFORMARCH="${PLATFORMARCH:-darwin/arm64}"

./local-ai util create-oci-image \
        build/darwin/. \
        --output ./backend-images/llama-cpp.tar \
        --image-name $IMAGE_NAME \
        --platform $PLATFORMARCH

rm -rf build/darwin

