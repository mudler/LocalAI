#!/bin/bash

set -ex

IMAGE_NAME="${IMAGE_NAME:-localai/cachyllama-darwin}"

pushd backend/cpp/cachyllama

# CachyLLaMA's ARM CPU_ALL_VARIANTS build includes SME variants that do not build
# reliably across the Darwin toolchains used by backend CI. The fallback build
# remains Metal-enabled on Darwin and is fully linked.
make cachyllama-fallback && \
make cachyllama-grpc && \
make cachyllama-rpc-server

popd

mkdir -p build/darwin
mkdir -p backend-images
mkdir -p build/darwin/lib

cp -rf backend/cpp/cachyllama/cachyllama-fallback build/darwin/
cp -rf backend/cpp/cachyllama/cachyllama-grpc build/darwin/
cp -rf backend/cpp/cachyllama/cachyllama-rpc-server build/darwin/

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


cp -rf backend/cpp/cachyllama/run.sh build/darwin/

PLATFORMARCH="${PLATFORMARCH:-darwin/arm64}"

./local-ai util create-oci-image \
        build/darwin/. \
        --output ./backend-images/cachyllama.tar \
        --image-name $IMAGE_NAME \
        --platform $PLATFORMARCH

rm -rf build/darwin
