#!/bin/bash
set -euo pipefail
BACKEND_DIR=$(cd -- "$(dirname -- "$0")" && pwd)
export GEMX_MODULE="$BACKEND_DIR/lib"
export GEMX_DEVICE=CPU
if [ -f "$BACKEND_DIR/lib/libggml-vulkan.so" ]; then export GEMX_DEVICE=Vulkan; fi
# Precision must be fixed before the first process-wide GGML initialization.
export GGML_VK_DISABLE_F16=1 GGML_VK_DISABLE_COOPMAT=1 GGML_VK_DISABLE_COOPMAT2=1
if [ "$(uname -s)" = Darwin ]; then
  export GEMX_LIBRARY="$BACKEND_DIR/lib/libgemx.dylib"
  export DYLD_LIBRARY_PATH="$BACKEND_DIR/lib:${DYLD_LIBRARY_PATH:-}"
else
  export GEMX_LIBRARY="$BACKEND_DIR/lib/libgemx.so"
  export LD_LIBRARY_PATH="$BACKEND_DIR/lib:${LD_LIBRARY_PATH:-}"
fi
if [ -f "$BACKEND_DIR/lib/ld.so" ]; then
  exec "$BACKEND_DIR/lib/ld.so" "$BACKEND_DIR/gemxcpp" "$@"
fi
exec "$BACKEND_DIR/gemxcpp" "$@"
