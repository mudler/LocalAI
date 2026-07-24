#!/bin/bash
# Script to package the core system runtime libraries based on architecture.
# This script copies the CPU-side runtime (libc/libstdc++/libgcc_s/libm/libgomp/
# libdl/librt/libpthread + the dynamic loader) into a backend's target lib
# directory so backends run self-contained through their bundled lib/ld.so.
#
# This is the CPU-side counterpart to scripts/build/package-gpu-libs.sh and is
# sourced by backend package.sh files the same way. It consolidates the inline
# arch-detect-and-copy block that was duplicated across backends, where each
# copy had drifted: several backends listed libgcc_s.so.1 / libstdc++.so.6
# twice, and a few omitted libgomp.so.1 (which OpenMP consumers dlopen rather
# than link, so the missing copy only fails at runtime). The shared copy lists
# each library exactly once and always includes libgomp.
#
# Usage: source package-system-libs.sh <lib_dest_dir> [binary_for_darwin_rpath]
#   $1 = absolute path to the destination lib directory (created if absent).
#   $2 = optional path to a packaged binary that needs @loader_path/lib added
#        as an LC_RPATH on Darwin so its @rpath-bundled dylibs resolve to
#        package/lib/. Empty or absent skips the rpath step (no-op on Darwin).

set -e

DEST="${1:?missing lib destination directory}"
BIN_FOR_RPATH="${2:-}"

mkdir -p "$DEST"

if [ "$(uname)" = "Darwin" ]; then
    # macOS has no glibc loader to bundle; system libraries are linked
    # dynamically. Bundled dylibs that reference @rpath need an LC_RPATH
    # pointing at lib/, otherwise dyld aborts with "no LC_RPATH's found".
    echo "Detected Darwin; system libraries linked dynamically, no bundled loader needed"
    if [ -n "$BIN_FOR_RPATH" ]; then
        # Tolerate the case where the binary already carries this rpath.
        install_name_tool -add_rpath @loader_path/lib "$BIN_FOR_RPATH" 2>/dev/null || true
    fi
elif [ -f "/lib64/ld-linux-x86-64.so.2" ]; then
    echo "Detected x86_64 architecture, copying x86_64 libraries..."
    cp -arfLv /lib64/ld-linux-x86-64.so.2 "$DEST/ld.so"
    cp -arfLv /lib/x86_64-linux-gnu/libc.so.6 "$DEST/libc.so.6"
    cp -arfLv /lib/x86_64-linux-gnu/libgcc_s.so.1 "$DEST/libgcc_s.so.1"
    cp -arfLv /lib/x86_64-linux-gnu/libstdc++.so.6 "$DEST/libstdc++.so.6"
    cp -arfLv /lib/x86_64-linux-gnu/libm.so.6 "$DEST/libm.so.6"
    cp -arfLv /lib/x86_64-linux-gnu/libgomp.so.1 "$DEST/libgomp.so.1"
    cp -arfLv /lib/x86_64-linux-gnu/libdl.so.2 "$DEST/libdl.so.2"
    cp -arfLv /lib/x86_64-linux-gnu/librt.so.1 "$DEST/librt.so.1"
    cp -arfLv /lib/x86_64-linux-gnu/libpthread.so.0 "$DEST/libpthread.so.0"
elif [ -f "/lib/ld-linux-aarch64.so.1" ]; then
    echo "Detected ARM64 architecture, copying ARM64 libraries..."
    cp -arfLv /lib/ld-linux-aarch64.so.1 "$DEST/ld.so"
    cp -arfLv /lib/aarch64-linux-gnu/libc.so.6 "$DEST/libc.so.6"
    cp -arfLv /lib/aarch64-linux-gnu/libgcc_s.so.1 "$DEST/libgcc_s.so.1"
    cp -arfLv /lib/aarch64-linux-gnu/libstdc++.so.6 "$DEST/libstdc++.so.6"
    cp -arfLv /lib/aarch64-linux-gnu/libm.so.6 "$DEST/libm.so.6"
    cp -arfLv /lib/aarch64-linux-gnu/libgomp.so.1 "$DEST/libgomp.so.1"
    cp -arfLv /lib/aarch64-linux-gnu/libdl.so.2 "$DEST/libdl.so.2"
    cp -arfLv /lib/aarch64-linux-gnu/librt.so.1 "$DEST/librt.so.1"
    cp -arfLv /lib/aarch64-linux-gnu/libpthread.so.0 "$DEST/libpthread.so.0"
else
    echo "Error: Could not detect architecture" >&2
    exit 1
fi
