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
# The CPU variants come from the cpu-variants stage of the Dockerfile (the fork's own pin,
# plain gcc); the tree built here has no CPU backend and ggml dlopens the best variant at
# run time. See .docker/llama-cpp-compile.sh.
CPU_VARIANTS_DIR=/LocalAI/backend/cpp/turboquant/cpu-variants
ls "$CPU_VARIANTS_DIR"/libggml-cpu-*.so > /dev/null
make "$BUILD_TARGET" CPU_VARIANTS_DIR="$CPU_VARIANTS_DIR"
make turboquant-grpc
make turboquant-rpc-server

ccache -s || true
