#!/bin/bash
# Darwin/Metal build for the privacy-filter backend. Mirrors ds4-darwin.sh:
# native make of the single grpc-server, otool -L dylib bundling, then assemble
# an OCI tar that `local-ai backends install` can consume.
#
# privacy-filter.cpp pulls ggml, which defaults GGML_METAL=ON on Apple - the
# engine's CMake never forces it off, so a plain Darwin build is Metal-enabled.
# grpc++/protobuf are resolved from Homebrew via find_package(... CONFIG).
set -ex

IMAGE_NAME="${IMAGE_NAME:-localai/privacy-filter-darwin}"

pushd backend/cpp/privacy-filter
make grpc-server
popd

mkdir -p build/darwin
mkdir -p build/darwin/lib
mkdir -p backend-images

cp -rf backend/cpp/privacy-filter/grpc-server build/darwin/
cp -rf backend/cpp/privacy-filter/run.sh      build/darwin/

# Apple Silicon: pick up Homebrew-installed protobuf utf8_validity if present
# (same as ds4-darwin.sh - it is a transitive dep otool may not surface).
if [[ "$(uname -s)" == "Darwin" && "$(uname -m)" == "arm64" ]]; then
    ADDITIONAL_LIBS=${ADDITIONAL_LIBS:-$(ls /opt/homebrew/Cellar/protobuf/**/lib/libutf8_validity*.dylib 2>/dev/null)}
else
    ADDITIONAL_LIBS=${ADDITIONAL_LIBS:-""}
fi
for file in $ADDITIONAL_LIBS; do
    cp -rfv "$file" build/darwin/lib
done

# Walk dylibs via otool -L and bundle anything that isn't a system framework.
for file in build/darwin/grpc-server; do
    LIBS="$(otool -L "$file" | awk 'NR > 1 { system("echo " $1) } ' | xargs echo)"
    for lib in $LIBS; do
        if [[ "$lib" == *.dylib ]] && [[ -e "$lib" ]]; then
            cp -rvf "$lib" build/darwin/lib
        fi
    done
done

echo "Bundled libraries:"
ls -la build/darwin/lib

# Build an OCI image tar (with manifest.json) that `local-ai backends install`
# can consume - mirrors ds4-darwin.sh.
PLATFORMARCH="${PLATFORMARCH:-darwin/arm64}"

./local-ai util create-oci-image \
        build/darwin/. \
        --output ./backend-images/privacy-filter.tar \
        --image-name "$IMAGE_NAME" \
        --platform "$PLATFORMARCH"

rm -rf build/darwin
