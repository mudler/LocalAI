#!/bin/bash

set -ex

export PORTABLE_PYTHON=true
export BUILD_TYPE=mps
export USE_PIP=true
IMAGE_NAME="${IMAGE_NAME:-localai/llama-cpp-darwin}"
mkdir -p backend-images
make -C backend/python/${BACKEND}

cp -rfv backend/python/common backend/python/${BACKEND}/

PLATFORMARCH="${PLATFORMARCH:-darwin/arm64}"

./local-ai util create-oci-image \
        backend/python/${BACKEND}/. \
        --output ./backend-images/${BACKEND}.tar \
        --image-name $IMAGE_NAME \
        --platform $PLATFORMARCH

make -C backend/python/${BACKEND} clean

