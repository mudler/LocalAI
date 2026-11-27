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

# Install here, not only in the base builder: existing prebuilt bases must
# acquire the strict decision decoders too. Runtime libraries are collected by
# the existing backend dependency packaging.
sh /LocalAI/.docker/apt-mirror.sh || true
sh /LocalAI/.docker/apt-mirror.sh apt-get update -qq && sh /LocalAI/.docker/apt-mirror.sh apt-get install -y --no-install-recommends libjpeg-dev zlib1g-dev

cd /LocalAI/backend/cpp/llama-cpp
BUILD_TARGET=$(/LocalAI/.docker/llama-cpp-build-target.sh "${TARGETARCH}" "${BUILD_TYPE:-}")
if [ "$BUILD_TARGET" = "llama-cpp-cpu-all" ]; then
  # One build replaces the per-microarch binaries: the loader plus the GPU backend
  # (when there is one) are built here with the CPU backend switched off, and ggml
  # dlopens the best libggml-cpu-*.so at run time for the work that stays on the
  # host. Those variants come from the cpu-variants stage of the Dockerfile, built
  # once per architecture with plain gcc, so no compiler of the GPU toolchain (icpx,
  # hipcc) compiles them and no builder needs gcc-14 for the arm64 ones.
  CPU_VARIANTS_DIR=/LocalAI/backend/cpp/llama-cpp/cpu-variants
  ls "$CPU_VARIANTS_DIR"/libggml-cpu-*.so > /dev/null
  set -- "CPU_VARIANTS_DIR=$CPU_VARIANTS_DIR"
else
  set --
fi
# llguidance, the grammar engine of the grammar_engine model option, is a Rust
# library that llama.cpp builds with cargo. The Rust toolchain is installed only
# here, in the builder: the image ships the statically linked library, not Rust.
# LLGUIDANCE=OFF builds without it, as before.
LLGUIDANCE="${LLGUIDANCE:-ON}"
if [ "$LLGUIDANCE" = "ON" ]; then
  export RUSTUP_HOME=/root/.rustup CARGO_HOME=/root/.cargo
  export PATH="$CARGO_HOME/bin:$PATH"
  RUST_TOOLCHAIN="${RUST_TOOLCHAIN:-1.100.0}"
  if ! rustup toolchain list 2>/dev/null | grep -q "^${RUST_TOOLCHAIN}"; then
    curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | \
      sh -s -- -y --no-modify-path --profile minimal --default-toolchain "$RUST_TOOLCHAIN"
  fi
  rustup default "$RUST_TOOLCHAIN"
  cargo --version
  set -- "$@" LLGUIDANCE=ON
fi
# CPU_ALL_RPC=ON builds the RPC backend and ggml-rpc-server in the same tree, so
# llama-cpp-grpc and llama-cpp-rpc-server come out of this one build. Building them in their own
# static tree compiled the whole GPU backend a second time, which was half the job time.
make "$BUILD_TARGET" "$@" CPU_ALL_RPC=ON
test -x llama-cpp-grpc && test -x llama-cpp-rpc-server

ccache -s || true
