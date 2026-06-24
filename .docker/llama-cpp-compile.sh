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
  # arm64: ggml's CPU_ALL_VARIANTS table includes armv9.2 SME variants whose
  # -march=...+sme is rejected by the Ubuntu 24.04 default gcc-13. gcc-14 accepts it, so
  # build the arm64 variants with gcc-14 (the host never *selects* SME unless it has it,
  # but every variant must still compile).
  if [ "${TARGETARCH}" = "arm64" ]; then
    apt-get update -qq && apt-get install -y -qq gcc-14 g++-14
    export CC=gcc-14 CXX=g++-14
  fi
  # x86 and arm64: one build with ggml CPU_ALL_VARIANTS replaces the per-microarch
  # binaries (x86: avx/avx2/avx512/fallback; arm64: armv8.x/armv9.x). ggml dlopens the
  # best libggml-cpu-*.so at runtime by probing host CPU features.
  make llama-cpp-cpu-all
fi
make llama-cpp-grpc
make llama-cpp-rpc-server

ccache -s || true
