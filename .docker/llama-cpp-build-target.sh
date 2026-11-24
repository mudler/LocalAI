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
# which is fifteen architectures (gfx908 through gfx1201, four of them the
# RDNA3/3.5 APUs added with ROCm 10.1), and the CPU variant matrix lands on
# top of that. The job built in 2h27m (with eleven) before it was added and
# has been killed at exactly 6h00m on every run since, so no ROCm llama-cpp
# image has been published since 2026-08-01.
case "$build_type" in
  sycl*|hipblas*)
    echo llama-cpp-fallback
    exit 0
    ;;
esac

# arm64 GPU images use cpu-all too. The portable fallback is built with
# GGML_NATIVE=OFF and has no Arm dotprod, i8mm or SVE kernels, so the CPU side of a
# partly offloaded model ran about 27% slower than a native build.
echo llama-cpp-cpu-all
