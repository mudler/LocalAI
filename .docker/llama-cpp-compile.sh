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
BUILD_TARGET=$(/LocalAI/.docker/llama-cpp-build-target.sh "${TARGETARCH}" "${BUILD_TYPE:-}")
if [ "$BUILD_TARGET" = "llama-cpp-cpu-all" ]; then
  # One build with ggml CPU_ALL_VARIANTS replaces the per-microarch binaries (x86:
  # avx/avx2/avx512/fallback; arm64: armv8.x/armv9.x). BUILD_TYPE remains in the
  # environment, so GPU builds retain their accelerator backend while ggml dlopens the
  # best CPU library when work is offloaded to the host.
  #
  # arm64: the CPU_ALL_VARIANTS table includes armv9.2 SME variants whose -march=...+sme is
  # rejected by the Ubuntu 24.04 default gcc-13. gcc-14 accepts it, so build the arm64
  # variants with it (the host never *selects* SME unless it has it, but every variant must
  # still compile).
  if [ "${TARGETARCH}" = "arm64" ]; then
    # The prebuilt base inherits default ports.ubuntu.com sources; honor the
    # APT_*_MIRROR build args here like the from-source path does, so this
    # apt step survives a mirror outage.
    sh /LocalAI/.docker/apt-mirror.sh || true
    apt-get update -qq && apt-get install -y -qq gcc-14 g++-14
    export CC=gcc-14 CXX=g++-14
  fi
fi
make "$BUILD_TARGET"
make llama-cpp-grpc
make llama-cpp-rpc-server

ccache -s || true
