#!/usr/bin/env bash
set -euo pipefail

CURDIR=$(dirname "$(realpath "$0")")
SELECTOR="$CURDIR/../../.docker/llama-cpp-build-target.sh"

assert_target() {
  local arch=$1
  local build_type=$2
  local expected=$3
  local actual

  actual=$("$SELECTOR" "$arch" "$build_type")
  if [ "$actual" != "$expected" ]; then
    echo "FAIL: $arch/$build_type selected $actual, expected $expected"
    exit 1
  fi
}

# One mechanism for every flavor: the CPU variants come from their own gcc stage,
# so no flavor falls back to the static binary.
for arch in amd64 arm64; do
  for build_type in "" cublas vulkan hipblas sycl_f16 sycl_f32; do
    assert_target "$arch" "$build_type" llama-cpp-cpu-all
  done
done

# The variants must not be compiled by a GPU toolchain: the selected target has to
# take them prebuilt and switch the CPU backend off in the GPU tree.
MAKEFILE="$CURDIR/../../backend/cpp/llama-cpp/Makefile"
grep -q -- '-DGGML_CPU=OFF' "$MAKEFILE" || { echo "FAIL: Makefile does not build the GPU tree with -DGGML_CPU=OFF"; exit 1; }
grep -q 'CPU_VARIANTS_DIR' "$CURDIR/../../.docker/llama-cpp-compile.sh" || { echo "FAIL: compile script does not pass CPU_VARIANTS_DIR"; exit 1; }

# The RPC binaries must come out of the cpu-all tree. A separate static grpc tree
# compiles the whole GPU backend (every CUDA or HIP arch) a second time, which
# doubled the GPU image builds and pushed the ROCm ones to the 6h job limit.
for fork in llama-cpp bonsai turboquant olala; do
  script="$CURDIR/../../.docker/${fork}-compile.sh"
  grep -q 'CPU_ALL_RPC=ON' "$script" || { echo "FAIL: $script does not build the RPC binaries in the cpu-all tree"; exit 1; }
  if grep -Eq "^make ${fork}-(grpc|rpc-server)" "$script"; then
    echo "FAIL: $script builds a second, static grpc tree"; exit 1
  fi
  grep -q 'CPU_ALL_RPC_TARGET' "$CURDIR/../../backend/cpp/${fork}/Makefile" || { echo "FAIL: backend/cpp/${fork}/Makefile has no CPU_ALL_RPC mode"; exit 1; }
done

echo "PASS: every llama.cpp image flavor selects the CPU variant build"
