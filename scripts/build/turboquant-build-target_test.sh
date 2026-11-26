#!/usr/bin/env bash
set -euo pipefail

CURDIR=$(dirname "$(realpath "$0")")
SELECTOR="$CURDIR/../../.docker/turboquant-build-target.sh"

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

# One mechanism for every flavor: the CPU variants come from their own gcc stage.
for arch in amd64 arm64; do
  for build_type in "" cublas vulkan hipblas sycl_f16 sycl_f32; do
    assert_target "$arch" "$build_type" turboquant-cpu-all
  done
done

echo "PASS: every turboquant image flavor selects the CPU variant build"
