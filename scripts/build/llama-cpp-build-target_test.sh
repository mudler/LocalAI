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

echo "PASS: llama.cpp build target preserves CPU variants where supported"
