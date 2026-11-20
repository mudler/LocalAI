#!/bin/bash
set -euo pipefail
BACKEND_DIR=$(cd -- "$(dirname -- "$0")" && pwd)
if [ "$(uname -s)" = Darwin ]; then
  export MESHREMESH_LIBRARY="$BACKEND_DIR/lib/libmeshremesh.dylib"
  export DYLD_LIBRARY_PATH="$BACKEND_DIR/lib:${DYLD_LIBRARY_PATH:-}"
else
  export MESHREMESH_LIBRARY="$BACKEND_DIR/lib/libmeshremesh.so"
  export LD_LIBRARY_PATH="$BACKEND_DIR/lib:${LD_LIBRARY_PATH:-}"
fi
if [ -f "$BACKEND_DIR/lib/ld.so" ]; then
  exec "$BACKEND_DIR/lib/ld.so" "$BACKEND_DIR/meshremesh" "$@"
fi
exec "$BACKEND_DIR/meshremesh" "$@"
