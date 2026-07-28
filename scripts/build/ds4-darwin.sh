#!/bin/bash
# Darwin/Metal build for the ds4 backend. Mirrors llama-cpp-darwin.sh:
# native make, otool -L for dylib bundling, then assemble an OCI tar that
# `local-ai backends install` can consume.
set -ex

IMAGE_NAME="${IMAGE_NAME:-localai/ds4-darwin}"

pushd backend/cpp/ds4
make NATIVE=false grpc-server package
popd

mkdir -p build/darwin
mkdir -p build/darwin/lib
mkdir -p backend-images

cp -rf backend/cpp/ds4/grpc-server build/darwin/
cp -rf backend/cpp/ds4/ds4-worker  build/darwin/
cp -rf backend/cpp/ds4/run.sh      build/darwin/

# Apple Silicon: pick up Homebrew-installed protobuf utf8_validity if present.
if [[ "$(uname -s)" == "Darwin" && "$(uname -m)" == "arm64" ]]; then
    ADDITIONAL_LIBS=${ADDITIONAL_LIBS:-$(ls /opt/homebrew/Cellar/protobuf/**/lib/libutf8_validity*.dylib 2>/dev/null)}
else
    ADDITIONAL_LIBS=${ADDITIONAL_LIBS:-""}
fi
for file in $ADDITIONAL_LIBS; do
    cp -rfv "$file" build/darwin/lib
done

# Walk dylibs via otool -L and bundle anything that isn't a system framework.
for file in build/darwin/grpc-server build/darwin/ds4-worker; do
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
# can consume - mirrors llama-cpp-darwin.sh. The previously referenced
# scripts/build/oci-pack.sh helper was never added to the tree, so the
# plain-tar fallback produced a manifest-less tarball the installer rejects
# with "file manifest.json not found in tar".
PLATFORMARCH="${PLATFORMARCH:-darwin/arm64}"

./local-ai util create-oci-image \
        build/darwin/. \
        --output ./backend-images/ds4.tar \
        --image-name "$IMAGE_NAME" \
        --platform "$PLATFORMARCH"

rm -rf build/darwin
