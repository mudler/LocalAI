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

assert_target amd64 cublas llama-cpp-cpu-all
assert_target amd64 vulkan llama-cpp-cpu-all
assert_target amd64 "" llama-cpp-cpu-all
assert_target arm64 cublas llama-cpp-fallback
assert_target arm64 "" llama-cpp-cpu-all

# SYCL builds the whole tree with icpx -fsycl, and icpx never finishes
# ggml-cpu/arch/x86/repack.cpp at -march=sapphirerapids: every sycl job sat on
# that one translation unit until GitHub killed it at its 6h limit.
assert_target amd64 sycl_f16 llama-cpp-fallback
assert_target amd64 sycl_f32 llama-cpp-fallback

# ROCm exhausts the same 6h budget through volume rather than a stall: hipcc
# compiles ggml's HIP kernels once per AMDGPU target, eleven of them, and the
# CPU variant matrix goes on top. 2h27m before it was added, killed at exactly
# 6h00m on every run since.
assert_target amd64 hipblas llama-cpp-fallback
assert_target arm64 hipblas llama-cpp-fallback

echo "PASS: llama.cpp build target preserves CPU variants where supported"
