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

# cp -rf backend/cpp/llama-cpp/llama-cpp-avx build/darwin/
# cp -rf backend/cpp/llama-cpp/llama-cpp-avx2 build/darwin/
# cp -rf backend/cpp/llama-cpp/llama-cpp-avx512 build/darwin/
cp -rf backend/cpp/llama-cpp/llama-cpp-fallback build/darwin/
cp -rf backend/cpp/llama-cpp/llama-cpp-grpc build/darwin/
cp -rf backend/cpp/llama-cpp/llama-cpp-rpc-server build/darwin/

for file in build/darwin/*; do
  LIBS="$(otool -L $file | awk 'NR > 1 { system("echo " $1) } ' | xargs echo)"

  for lib in $LIBS; do
    mkdir -p build/darwin/lib
    # only libraries ending in dylib
    if [[ "$lib" == *.dylib ]]; then
        if [ -e "$lib" ]; then
            cp -rvf "$lib" build/darwin/lib
        fi
    fi
  done
done

cp -rf backend/cpp/llama-cpp/run.sh build/darwin/

PLATFORMARCH="${PLATFORMARCH:-darwin/arm64}"

./local-ai util create-oci-image \
        build/darwin/. \
        --output build/darwin.tar \
        --image-name $IMAGE_NAME \
        --platform $PLATFORMARCH

rm -rf build/darwin

