#!/usr/bin/env bash
set -euo pipefail

arch=${1:?target architecture is required}
build_type=${2-}

# SYCL compiles the whole tree with icpx -fsycl, and icpx never finishes
# ggml-cpu/arch/x86/repack.cpp at -march=sapphirerapids: the job sits on that one
# translation unit until GitHub kills it at 6h. gcc builds the same file in
# seconds, so only the SYCL images have to give up the CPU variant matrix.
case "$build_type" in
  sycl*)
    echo turboquant-fallback
    exit 0
    ;;
esac

# GPU arm64 base images do not consistently provide the gcc-14 toolchain needed
# to compile ggml's armv9.2 CPU variants. Keep their portable fallback until the
# builder images can supply that compiler.
if [ "$arch" = "arm64" ] && [ -n "$build_type" ]; then
  echo turboquant-fallback
else
  echo turboquant-cpu-all
fi
