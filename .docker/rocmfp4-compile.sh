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

# rocmfp4 is ROCm-only: the single matrix entry is hipblas/amd64, so BUILD_TYPE is always
# set and the CPU-image branch this was templated from is unreachable. One fallback CPU
# build, the accelerator does the compute.
make rocmfp4-fallback
make rocmfp4-grpc
make rocmfp4-rpc-server
make rocmfp4-quantize

ccache -s || true
