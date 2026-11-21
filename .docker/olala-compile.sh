#!/usr/bin/env bash
# Shared compile logic for backend/Dockerfile.olala.
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
  rm -rf /LocalAI/backend/cpp/olala-*-build
fi

# The shared grpc-server CMakeLists (backend/cpp/llama-cpp) needs JPEG and zlib dev
# packages (find_package(JPEG/ZLIB REQUIRED)). Install them here, not only in the base
# builder: existing prebuilt bases do not have them. Same step as llama-cpp-compile.sh.
sh /LocalAI/.docker/apt-mirror.sh || true
sh /LocalAI/.docker/apt-mirror.sh apt-get update -qq && sh /LocalAI/.docker/apt-mirror.sh apt-get install -y --no-install-recommends libjpeg-dev zlib1g-dev

cd /LocalAI/backend/cpp/olala

if [ -z "${BUILD_TYPE:-}" ]; then
  # Pure CPU image: one ggml CPU_ALL_VARIANTS build replaces the per-microarch binaries.
  # arm64: the armv9.2 SME variants need gcc-14 (gcc-13 rejects +sme).
  if [ "${TARGETARCH}" = "arm64" ]; then
    sh /LocalAI/.docker/apt-mirror.sh || true
    sh /LocalAI/.docker/apt-mirror.sh apt-get update -qq && sh /LocalAI/.docker/apt-mirror.sh apt-get install -y -qq gcc-14 g++-14
    export CC=gcc-14 CXX=g++-14
  fi
  make olala-cpu-all
else
  # GPU build (cublas/hipblas/sycl/vulkan/...): single fallback CPU build, the accelerator
  # does the compute. Keeps the GPU compile from also building the CPU variant matrix and
  # avoids the gcc-14 apt step on GPU base images such as nvidia l4t.
  make olala-fallback
fi
make olala-grpc
make olala-rpc-server

ccache -s || true
