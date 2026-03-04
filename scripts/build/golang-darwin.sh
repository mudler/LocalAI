#!/bin/bash -eux

export BUILD_TYPE="${BUILD_TYPE:-metal}"

mkdir -p backend-images
make -C backend/go/${BACKEND} build

PLATFORMARCH="${PLATFORMARCH:-darwin/arm64}"
IMAGE_NAME="${IMAGE_NAME:-localai/${BACKEND}-darwin}"

./local-ai util create-oci-image \
        backend/go/${BACKEND}/. \
        --output ./backend-images/${BACKEND}.tar \
        --image-name $IMAGE_NAME \
        --platform $PLATFORMARCH

make -C backend/go/${BACKEND} clean
