#!/usr/bin/env bash
# Shared compile logic for backend/Dockerfile.bonsai.
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
  rm -rf /LocalAI/backend/cpp/bonsai-*-build
fi

cd /LocalAI/backend/cpp/bonsai

# The CPU variants come from the cpu-variants stage of the Dockerfile (the fork's own pin,
# plain gcc); the tree built here has no CPU backend and ggml dlopens the best variant at
# run time, for CPU-only and GPU images alike. See .docker/llama-cpp-compile.sh.
CPU_VARIANTS_DIR=/LocalAI/backend/cpp/bonsai/cpu-variants
ls "$CPU_VARIANTS_DIR"/libggml-cpu-*.so > /dev/null
# CPU_ALL_RPC=ON builds the RPC backend and ggml-rpc-server in the same tree, so
# bonsai-grpc and bonsai-rpc-server come out of this one build. Building them in their own
# static tree compiled the whole GPU backend a second time, which was half the job time.
make bonsai-cpu-all CPU_VARIANTS_DIR="$CPU_VARIANTS_DIR" CPU_ALL_RPC=ON
test -x bonsai-grpc && test -x bonsai-rpc-server

ccache -s || true
