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

if [ "${BUILD_TYPE}" = "hipblas" ]; then
  # ROCm: single fallback CPU build (GPU does the compute).
  make turboquant-fallback
else
  # x86 and arm64: one ggml CPU_ALL_VARIANTS build replaces the per-microarch binaries.
  make turboquant-cpu-all
fi
make turboquant-grpc
make turboquant-rpc-server

ccache -s || true
