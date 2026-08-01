#!/usr/bin/env bash
# Shared compile logic for backend/Dockerfile.turboquant.
# Sourced (via bind mount) from both builder-fromsource and builder-prebuilt stages.

set -euxo pipefail

export CCACHE_DIR=/root/.ccache
ccache --max-size=5G || true
ccache -z || true

export CMAKE_ARGS="${CMAKE_ARGS:-} -DCMAKE_C_COMPILER_LAUNCHER=ccache -DCMAKE_CXX_COMPILER_LAUNCHER=ccache -DCMAKE_CUDA_COMPILER_LAUNCHER=ccache"

if [[ -n "${CUDA_DOCKER_ARCH:-}" ]]; then
  CUDA_ARCH_ESC="${CUDA_DOCKER_ARCH//;/\\;}"
  export CMAKE_ARGS="${CMAKE_ARGS} -DCMAKE_CUDA_ARCHITECTURES=${CUDA_ARCH_ESC}"
  echo "CMAKE_ARGS(env) = ${CMAKE_ARGS}"
  rm -rf /LocalAI/backend/cpp/turboquant-*-build
fi

cd /LocalAI/backend/cpp/turboquant

BUILD_TARGET=$(/LocalAI/.docker/turboquant-build-target.sh "${TARGETARCH}" "${BUILD_TYPE:-}")
if [ "$BUILD_TARGET" = "turboquant-cpu-all" ]; then
  # BUILD_TYPE remains in the environment, so GPU builds retain their accelerator while
  # ggml selects the best CPU library when model work is offloaded to the host.
  # arm64: the armv9.2 SME variants need gcc-14 (gcc-13 rejects +sme).
  if [ "${TARGETARCH}" = "arm64" ]; then
    sh /LocalAI/.docker/apt-mirror.sh || true
    apt-get update -qq && apt-get install -y -qq gcc-14 g++-14
    export CC=gcc-14 CXX=g++-14
  fi
fi
make "$BUILD_TARGET"
make turboquant-grpc
make turboquant-rpc-server

ccache -s || true
