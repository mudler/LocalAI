#!/bin/bash

BUILD_TYPE?=""
CUDA_MAJOR_VERSION?="11"
CUDA_MINOR_VERSION?="7"
FFMPEG?=""
IMAGE_TYPE?=""
COMMIT_HASH?=$(git rev-parse HEAD)
VERSION?=$(git describe --always --tags || echo 'dev')

IMAGE_PREFIX?=""

docker build -t "${IMAGE_PREFIX}localai" \
    --build-arg BUILD_TYPE=$BUILD_TYPE \
    --build-arg CUDA_MAJOR_VERSION=$CUDA_MAJOR_VERSION --build-arg CUDA_MINOR_VERSION=$CUDA_MINOR_VERSION \
    --build-arg FFMPEG=$FFMPEG --build-arg IMAGE_TYPE=$IMAGE_TYPE \
    --build-arg COMMIT_HASH=$COMMIT_HASH --build-arg VERSION=$VERSION