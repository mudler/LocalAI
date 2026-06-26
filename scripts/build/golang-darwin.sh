#!/bin/bash -eux

export BUILD_TYPE="${BUILD_TYPE:-metal}"

mkdir -p backend-images
make -C backend/go/${BACKEND} build

BACKEND_DIR="backend/go/${BACKEND}"

# Never package a stray CMake build tree (e.g. build-libgo*-*.so/, a directory
# left behind by a partial native build) into the backend image.
rm -rf "${BACKEND_DIR}"/build-*

# Fail loudly if the build did not produce the backend binary, instead of
# silently packaging the source/build tree as a "backend" that can never start
# (issue #10267: the darwin vibevoice-cpp image shipped sources, no binary).
# run.sh's final `exec $CURDIR/<binary>` is the contract for what gets launched;
# the binary is not always named after the backend (e.g. parakeet-cpp launches
# parakeet-cpp-grpc), so derive it from run.sh and fall back to ${BACKEND}.
RUN_BINARY=""
if [ -f "${BACKEND_DIR}/run.sh" ]; then
        RUN_BINARY=$(grep -oE '\$CURDIR/[A-Za-z0-9._-]+' "${BACKEND_DIR}/run.sh" | grep -v 'ld\.so' | tail -1 | sed 's|\$CURDIR/||')
fi
RUN_BINARY="${RUN_BINARY:-${BACKEND}}"

# Ship the self-contained package/ dir (run.sh + binary + lib/), matching the
# Linux Dockerfile.golang (`COPY .../package/. ./`). Packaging the whole backend
# dir instead left the runtime libraries under package/lib while run.sh looks in
# $CURDIR/lib, so backends such as sherpa-onnx could not dlopen their libs at
# runtime (they started fine only when run from inside package/). Backends that
# don't assemble a package/ fall back to the backend dir.
OCI_ROOT="${BACKEND_DIR}"
if [ -d "${BACKEND_DIR}/package" ]; then
        OCI_ROOT="${BACKEND_DIR}/package"
fi

if [ ! -x "${OCI_ROOT}/${RUN_BINARY}" ]; then
        echo "ERROR: ${OCI_ROOT}/${RUN_BINARY} not found after build; refusing to package a broken backend image (see issue #10267)." >&2
        exit 1
fi

PLATFORMARCH="${PLATFORMARCH:-darwin/arm64}"
IMAGE_NAME="${IMAGE_NAME:-localai/${BACKEND}-darwin}"

./local-ai util create-oci-image \
        "${OCI_ROOT}/." \
        --output ./backend-images/${BACKEND}.tar \
        --image-name $IMAGE_NAME \
        --platform $PLATFORMARCH

make -C backend/go/${BACKEND} clean
