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
if [ -z "${BUILD_TYPE:-}" ]; then
  # Pure CPU image (BUILD_TYPE empty): one build with ggml CPU_ALL_VARIANTS replaces the
  # per-microarch binaries (x86: avx/avx2/avx512/fallback; arm64: armv8.x/armv9.x). ggml
  # dlopens the best libggml-cpu-*.so at runtime by probing host CPU features.
  #
  # arm64: the CPU_ALL_VARIANTS table includes armv9.2 SME variants whose -march=...+sme is
  # rejected by the Ubuntu 24.04 default gcc-13. gcc-14 accepts it, so build the arm64
  # variants with it (the host never *selects* SME unless it has it, but every variant must
  # still compile).
  if [ "${TARGETARCH}" = "arm64" ]; then
    apt-get update -qq && apt-get install -y -qq gcc-14 g++-14
    export CC=gcc-14 CXX=g++-14
  fi
  make llama-cpp-cpu-all
else
  # GPU build (cublas/hipblas/sycl/vulkan/...): the accelerator does the compute, so a
  # single fallback CPU build is enough - no per-microarch CPU variants needed. (This also
  # keeps the heavy GPU backend compile from also building the whole CPU variant matrix,
  # and avoids the gcc-14 apt step on GPU base images such as nvidia l4t.)
  make llama-cpp-fallback
fi
make llama-cpp-grpc
make llama-cpp-rpc-server

ccache -s || true
