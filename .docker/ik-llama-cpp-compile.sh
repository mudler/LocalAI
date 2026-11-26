#!/usr/bin/env bash
# Shared compile logic for backend/Dockerfile.ik-llama-cpp.
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
  rm -rf /LocalAI/backend/cpp/ik-llama-cpp-*-build
fi

cd /LocalAI/backend/cpp/ik-llama-cpp

# ik_llama.cpp keeps its own ggml: its CMake has neither GGML_BACKEND_DL nor
# GGML_CPU_ALL_VARIANTS, so there are no libggml-cpu-*.so modules to share and the
# cpu-variants stage of the other llama.cpp-based backends does not apply. This
# backend keeps one binary per instruction-set level (AVX2 minimum for the IQK kernels).

if [ "${TARGETARCH}" = "arm64" ] || [ "${BUILD_TYPE}" = "hipblas" ]; then
  # ARM64 / ROCm: build without x86 SIMD
  make ik-llama-cpp-fallback
else
  # ik_llama.cpp's IQK kernels require at least AVX2
  make ik-llama-cpp-avx2
fi

ccache -s || true
