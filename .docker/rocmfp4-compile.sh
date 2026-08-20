#!/usr/bin/env bash
# Shared compile logic for backend/Dockerfile.rocmfp4.
# Sourced (via bind mount) from both builder-fromsource and builder-prebuilt stages.

set -euxo pipefail

# Docker ARG defaults arrive as empty strings, and an empty-but-defined variable
# still beats Make's ?= assignment. Drop them so the Makefile's pin wins unless a
# local build genuinely overrides it.
[ -n "${LLAMA_REPO:-}" ] || unset LLAMA_REPO || true
[ -n "${ROCMFP4_VERSION:-}" ] || unset ROCMFP4_VERSION || true

export CCACHE_DIR=/root/.ccache
ccache --max-size=5G || true
ccache -z || true

export CMAKE_ARGS="${CMAKE_ARGS:-} -DCMAKE_C_COMPILER_LAUNCHER=ccache -DCMAKE_CXX_COMPILER_LAUNCHER=ccache -DCMAKE_CUDA_COMPILER_LAUNCHER=ccache"

if [[ -n "${CUDA_DOCKER_ARCH:-}" ]]; then
  CUDA_ARCH_ESC="${CUDA_DOCKER_ARCH//;/\\;}"
  export CMAKE_ARGS="${CMAKE_ARGS} -DCMAKE_CUDA_ARCHITECTURES=${CUDA_ARCH_ESC}"
  echo "CMAKE_ARGS(env) = ${CMAKE_ARGS}"
  rm -rf /LocalAI/backend/cpp/rocmfp4-*-build
fi

cd /LocalAI/backend/cpp/rocmfp4

if [ -z "${BUILD_TYPE:-}" ]; then
  # Pure CPU image: one ggml CPU_ALL_VARIANTS build replaces the per-microarch binaries.
  # arm64: the armv9.2 SME variants need gcc-14 (gcc-13 rejects +sme).
  if [ "${TARGETARCH}" = "arm64" ]; then
    apt-get update -qq && apt-get install -y -qq gcc-14 g++-14
    export CC=gcc-14 CXX=g++-14
  fi
  make rocmfp4-cpu-all
else
  # GPU build (cublas/hipblas/sycl/vulkan/...): single fallback CPU build, the accelerator
  # does the compute. Keeps the GPU compile from also building the CPU variant matrix and
  # avoids the gcc-14 apt step on GPU base images such as nvidia l4t.
  make rocmfp4-fallback
fi
make rocmfp4-grpc
make rocmfp4-rpc-server

ccache -s || true
