#!/usr/bin/env bash
# Shared compile logic for backend/Dockerfile.llama-cpp.
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
  rm -rf /LocalAI/backend/cpp/llama-cpp-*-build
fi

if [ "${TARGETARCH}" = "arm64" ] || [ "${BUILD_TYPE}" = "hipblas" ]; then
  cd /LocalAI/backend/cpp/llama-cpp
  make llama-cpp-fallback
  make llama-cpp-grpc
  make llama-cpp-rpc-server
else
  cd /LocalAI/backend/cpp/llama-cpp
  make llama-cpp-avx
  make llama-cpp-avx2
  make llama-cpp-avx512
  make llama-cpp-fallback
  make llama-cpp-grpc
  make llama-cpp-rpc-server
fi

ccache -s || true
