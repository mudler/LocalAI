#!/usr/bin/env bash
set -euo pipefail

# Every image flavor ships llama-cpp-cpu-all: one loader plus, for a GPU image, its
# own backend, with ggml choosing the CPU variant at run time. The CPU variants are
# built by their own stage (.docker/llama-cpp-cpu-variants.sh) with plain gcc, so the
# GPU toolchains never compile them. That is what used to force a static fallback:
#   - SYCL: icpx never finished ggml-cpu/arch/x86/repack.cpp at -march=sapphirerapids.
#   - ROCm: the CPU variant matrix, compiled by hipcc next to the HIP kernels of every
#     AMDGPU target, pushed the job past GitHub's 6h limit.
#   - arm64 GPU: the builder images lack the gcc-14 that ggml's armv9.2 variants need.
# The arguments stay so callers and the test keep one interface.
: "${1:?target architecture is required}"
echo llama-cpp-cpu-all
