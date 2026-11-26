#!/usr/bin/env bash
# Checks the NCCL part of a packaged llama-cpp backend.
#
# Usage: check-nccl-bundle.sh PACKAGE_DIR [NCCL_HEADER]
#
# Two failures that would otherwise only show on a multi-GPU host:
#   1. A packaged ELF needs libnccl.so.2 but lib/ does not carry it. Backends run
#      through the bundled loader with LD_LIBRARY_PATH=lib, so the library must be
#      in the package. With GGML_BACKEND_DL a missing NCCL makes ggml skip
#      libggml-cuda.so and the model silently runs on the CPU.
#   2. The builder has NCCL (NCCL_HEADER exists, default /usr/include/nccl.h) but
#      libggml-cuda.so does not link it: CMake did not find NCCL and tensor split
#      would quietly use the slower fallback.
# The NCCL license (BSD-3-Clause) asks binary copies to carry its notice, so the
# copyright file of the libnccl2 package goes next to the library.

set -euo pipefail

PKG_DIR="${1:?package directory is required}"
NCCL_HEADER="${2:-/usr/include/nccl.h}"
NCCL_COPYRIGHT="${NCCL_COPYRIGHT:-/usr/share/doc/libnccl2/copyright}"
LIB_DIR="$PKG_DIR/lib"

needs_nccl() {
    readelf -d "$1" 2>/dev/null | grep -q 'NEEDED.*\[libnccl\.so'
}

elfs=()
for f in "$PKG_DIR"/llama-cpp-* "$LIB_DIR"/*.so*; do
    [ -f "$f" ] && [ ! -L "$f" ] && elfs+=("$f")
done

linked=0
for f in "${elfs[@]}"; do
    if needs_nccl "$f"; then
        linked=1
        if ! ls "$LIB_DIR"/libnccl.so.2* >/dev/null 2>&1; then
            echo "NCCL: $(basename "$f") needs libnccl.so.2 but $LIB_DIR does not have it" >&2
            exit 1
        fi
    fi
done

if [ -f "$NCCL_HEADER" ]; then
    cuda_lib=""
    for f in "$LIB_DIR"/libggml-cuda.so*; do
        [ -f "$f" ] && [ ! -L "$f" ] && cuda_lib="$f" && break
    done
    if [ -n "$cuda_lib" ] && ! needs_nccl "$cuda_lib"; then
        echo "NCCL: $NCCL_HEADER exists but $(basename "$cuda_lib") does not link NCCL; check the CMake log for 'NCCL not found'" >&2
        exit 1
    fi
fi

if [ "$linked" = 1 ]; then
    if [ -f "$NCCL_COPYRIGHT" ]; then
        mkdir -p "$PKG_DIR/licenses"
        cp "$NCCL_COPYRIGHT" "$PKG_DIR/licenses/NCCL-LICENSE.txt"
    fi
    echo "NCCL: bundled ($(cd "$LIB_DIR" && ls libnccl.so.2*| tr '\n' ' '))"
else
    echo "NCCL: not linked"
fi
