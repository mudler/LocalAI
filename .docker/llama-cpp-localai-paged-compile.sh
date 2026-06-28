#!/usr/bin/env bash
# Shared compile logic for backend/Dockerfile.llama-cpp-localai-paged.
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
  rm -rf /LocalAI/backend/cpp/llama-cpp-localai-paged-*-build
fi

cd /LocalAI/backend/cpp/llama-cpp-localai-paged

if [ -z "${BUILD_TYPE:-}" ]; then
  # Pure CPU image: one ggml CPU_ALL_VARIANTS build replaces the per-microarch binaries.
  # arm64: the armv9.2 SME variants need gcc-14 (gcc-13 rejects +sme).
  if [ "${TARGETARCH}" = "arm64" ]; then
    apt-get update -qq && apt-get install -y -qq gcc-14 g++-14
    export CC=gcc-14 CXX=g++-14
  fi
  make llama-cpp-localai-paged-cpu-all
else
  # GPU build (cublas/hipblas/sycl/vulkan/...): single fallback CPU build, the accelerator
  # does the compute. Keeps the GPU compile from also building the CPU variant matrix and
  # avoids the gcc-14 apt step on GPU base images such as nvidia l4t.
  make llama-cpp-localai-paged-fallback
fi
make llama-cpp-localai-paged-grpc
make llama-cpp-localai-paged-rpc-server

ccache -s || true
