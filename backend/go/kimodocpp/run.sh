#!/bin/bash
set -euo pipefail

BACKEND_DIR=$(cd -- "$(dirname -- "$0")" && pwd)
if [ "$(uname -s)" = Darwin ]; then
  export KIMODO_LIBRARY="$BACKEND_DIR/lib/libkimodo.dylib"
  export DYLD_LIBRARY_PATH="$BACKEND_DIR/lib:${DYLD_LIBRARY_PATH:-}"
else
  export KIMODO_LIBRARY="$BACKEND_DIR/lib/libkimodo.so"
  CPU_LIBRARY_DIR="$BACKEND_DIR/lib"
  if [ -d "$BACKEND_DIR/variants/avx2" ] &&
      grep -qw avx2 /proc/cpuinfo && grep -qw fma /proc/cpuinfo &&
      grep -qw f16c /proc/cpuinfo && grep -qw bmi2 /proc/cpuinfo; then
    CPU_LIBRARY_DIR="$BACKEND_DIR/variants/avx2"
  fi
  export LD_LIBRARY_PATH="$CPU_LIBRARY_DIR:$BACKEND_DIR/lib:${LD_LIBRARY_PATH:-}"
fi

if [ -f "$BACKEND_DIR/lib/ld.so" ]; then
  exec "$BACKEND_DIR/lib/ld.so" "$BACKEND_DIR/kimodocpp" "$@"
fi
exec "$BACKEND_DIR/kimodocpp" "$@"
