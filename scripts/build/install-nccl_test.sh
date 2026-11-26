#!/usr/bin/env bash
# Tests the choices of .docker/install-nccl.sh: which images get NCCL, and which
# libnccl2 version is picked for a CUDA release. Needs only bash and awk.
set -euo pipefail

CURDIR=$(dirname "$(realpath "$0")")
# shellcheck source=/dev/null
source "$CURDIR/../../.docker/install-nccl.sh"

fail() { echo "FAIL: $*"; exit 1; }

expect_wanted() {
    nccl_wanted "$1" "$2" "$3" || fail "expected NCCL for build_type=$1 arch=$2 cuda=$3"
}
expect_not_wanted() {
    if nccl_wanted "$1" "$2" "$3"; then fail "expected no NCCL for build_type=$1 arch=$2 cuda=$3"; fi
}

expect_wanted cublas amd64 12
expect_wanted cublas amd64 13
expect_wanted cublas arm64 13
# CUDA 12 on arm64 is the JetPack (Jetson) image: no NCCL from NVIDIA there.
expect_not_wanted cublas arm64 12
expect_not_wanted l4t arm64 12
for bt in "" hipblas vulkan sycl_f16 sycl_f32; do
    expect_not_wanted "$bt" amd64 ""
done

MADISON=$(cat <<'M'
  libnccl2 | 2.28.9-1+cuda13.0 | https://example.invalid/repo  Packages
  libnccl2 | 2.32.6-1+cuda12.9 | https://example.invalid/repo  Packages
  libnccl2 | 2.26.2-1+cuda12.8 | https://example.invalid/repo  Packages
  libnccl2 | 2.25.1-1+cuda12.8 | https://example.invalid/repo  Packages
  libnccl2 | 2.9.6-1+cuda12.8 | https://example.invalid/repo  Packages
  libnccl2 | 2.28.7-1+cuda13.0 | https://example.invalid/repo  Packages
  libnccl2 | 2.29.3-1+cuda13.1 | https://example.invalid/repo  Packages
M
)

got=$(printf '%s\n' "$MADISON" | nccl_pick_version "+cuda12.8")
[ "$got" = "2.26.2-1+cuda12.8" ] || fail "CUDA 12.8 picked '$got'"
got=$(printf '%s\n' "$MADISON" | nccl_pick_version "+cuda13.0")
[ "$got" = "2.28.9-1+cuda13.0" ] || fail "CUDA 13.0 picked '$got'"
# A build for another minor release must never match.
got=$(printf '%s\n' "$MADISON" | nccl_pick_version "+cuda12.6")
[ -z "$got" ] || fail "CUDA 12.6 picked '$got', expected nothing"

echo "PASS: install-nccl.sh picks the right images and NCCL versions"
