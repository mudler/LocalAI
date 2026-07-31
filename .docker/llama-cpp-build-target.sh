#!/usr/bin/env bash
set -euo pipefail

arch=${1:?target architecture is required}
build_type=${2-}

# GPU arm64 base images do not consistently provide the gcc-14 toolchain needed
# to compile ggml's armv9.2 CPU variants. Keep their portable fallback until the
# builder images can supply that compiler.
if [ "$arch" = "arm64" ] && [ -n "$build_type" ]; then
  echo llama-cpp-fallback
else
  echo llama-cpp-cpu-all
fi
