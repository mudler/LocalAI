#!/bin/bash

set -ex

IMAGE_NAME="${IMAGE_NAME:-localai/llama-cpp-darwin}"

pushd backend/cpp/llama-cpp

# make llama-cpp-avx && \
# make llama-cpp-avx2 && \
# make llama-cpp-avx512 && \
make llama-cpp-fallback && \
make llama-cpp-grpc && \
make llama-cpp-rpc-server

popd

mkdir -p build/darwin
mkdir -p backend-images
mkdir -p build/darwin/lib

# cp -rf backend/cpp/llama-cpp/llama-cpp-avx build/darwin/
# cp -rf backend/cpp/llama-cpp/llama-cpp-avx2 build/darwin/
# cp -rf backend/cpp/llama-cpp/llama-cpp-avx512 build/darwin/
cp -rf backend/cpp/llama-cpp/llama-cpp-fallback build/darwin/
cp -rf backend/cpp/llama-cpp/llama-cpp-grpc build/darwin/
cp -rf backend/cpp/llama-cpp/llama-cpp-rpc-server build/darwin/

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

