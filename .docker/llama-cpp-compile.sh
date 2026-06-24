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

cd /LocalAI/backend/cpp/llama-cpp
if [ "${BUILD_TYPE}" = "hipblas" ]; then
  # ROCm: the GPU does the compute, so a single fallback CPU build is enough.
  make llama-cpp-fallback
else
  # x86 and arm64: one build with ggml CPU_ALL_VARIANTS replaces the per-microarch
  # binaries (x86: avx/avx2/avx512/fallback; arm64: armv8.x/armv9.x). ggml dlopens the
  # best libggml-cpu-*.so at runtime by probing host CPU features.
  make llama-cpp-cpu-all
fi
make llama-cpp-grpc
make llama-cpp-rpc-server

ccache -s || true
