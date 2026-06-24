#!/usr/bin/env bash
set -euo pipefail

arch=${1:?target architecture is required}
build_type=${2-}

# SYCL compiles the whole tree with icpx -fsycl, and icpx never finishes
# ggml-cpu/arch/x86/repack.cpp at -march=sapphirerapids: the job sits on that one
# translation unit until GitHub kills it at 6h. gcc builds the same file in
# seconds, so only the SYCL images have to give up the CPU variant matrix.
#
# ROCm runs out of the same 6h budget for a different reason: volume, not a
# stall. hipcc compiles ggml's HIP kernels once per entry in AMDGPU_TARGETS,
# which is eleven architectures (gfx908 through gfx1201), and the CPU variant
# matrix lands on top of that. The job built in 2h27m before it was added and
# has been killed at exactly 6h00m on every run since, so no ROCm llama-cpp
# image has been published since 2026-08-01.
case "$build_type" in
  sycl*|hipblas*)
    echo llama-cpp-fallback
    exit 0
    ;;
esac

# GPU arm64 base images do not consistently provide the gcc-14 toolchain needed
# to compile ggml's armv9.2 CPU variants. Keep their portable fallback until the
# builder images can supply that compiler.
if [ "$arch" = "arm64" ] && [ -n "$build_type" ]; then
  echo llama-cpp-fallback
else
  echo llama-cpp-cpu-all
fi
