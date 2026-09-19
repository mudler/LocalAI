#!/usr/bin/env bash
# Shared compile logic for backend/Dockerfile.cachyllama.
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
  rm -rf /LocalAI/backend/cpp/cachyllama-*-build
fi

cd /LocalAI/backend/cpp/cachyllama

if [ -z "${BUILD_TYPE:-}" ]; then
  # Keep arm64 on the portable, fully linked build. CachyLLaMA's ARM
  # CPU_ALL_VARIANTS build includes SME variants that do not build reliably
  # across the Linux and Darwin toolchains used by backend CI.
  if [ "${TARGETARCH}" = "arm64" ]; then
    make cachyllama-fallback
  else
    # One ggml CPU_ALL_VARIANTS build replaces the per-microarch x86 binaries.
    make cachyllama-cpu-all
  fi
else
  # GPU build (cublas/hipblas/sycl/vulkan/...): single fallback CPU build, the accelerator
  # does the compute. Keeps the GPU compile from also building the CPU variant matrix and
  # avoids the gcc-14 apt step on GPU base images such as nvidia l4t.
  make cachyllama-fallback
fi
make cachyllama-grpc
make cachyllama-rpc-server

ccache -s || true
