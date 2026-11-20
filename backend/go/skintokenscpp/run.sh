#!/bin/bash
set -euo pipefail

BACKEND_DIR=$(cd -- "$(dirname -- "$0")" && pwd)
if [ "$(uname -s)" = Darwin ]; then
  export SKINTOKENS_LIBRARY="$BACKEND_DIR/lib/libskintokens.dylib"
  export DYLD_LIBRARY_PATH="$BACKEND_DIR/lib:${DYLD_LIBRARY_PATH:-}"
else
  export SKINTOKENS_LIBRARY="$BACKEND_DIR/lib/libskintokens.so"
  export LD_LIBRARY_PATH="$BACKEND_DIR/lib:${LD_LIBRARY_PATH:-}"
fi

# GGML selects among upstream's portable CPU variants in the same directory as
# the Vulkan backend. A CPU-only search directory would hide the accelerator.
export SKINTOKENS_BACKEND_DIR="$BACKEND_DIR/lib"

if [ -f "$BACKEND_DIR/lib/ld.so" ]; then
  exec "$BACKEND_DIR/lib/ld.so" "$BACKEND_DIR/skintokenscpp" "$@"
fi
exec "$BACKEND_DIR/skintokenscpp" "$@"
