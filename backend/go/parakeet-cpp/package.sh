#!/bin/bash
#
# Bundle the parakeet-cpp-grpc binary, libparakeet.so, the core runtime
# libs (libc/libstdc++/libgomp + ld.so) and the GPU runtime for the active
# BUILD_TYPE so the package is self-contained. Mirrors
# backend/go/whisper/package.sh; run.sh routes the (CGO_ENABLED=0) binary
# through lib/ld.so so the packaged libc is used instead of the host's.

set -e

CURDIR=$(dirname "$(realpath "$0")")
REPO_ROOT="${CURDIR}/../../.."

mkdir -p "$CURDIR/package/lib"

cp -avf "$CURDIR/parakeet-cpp-grpc" "$CURDIR/package/"
cp -avf "$CURDIR/run.sh" "$CURDIR/package/"

# libparakeet shared lib + any soname symlinks. On Linux this is
# libparakeet.so[.X.Y]; on macOS it is libparakeet.dylib. purego.Dlopen
# resolves it via the *_LIBRARY_PATH that run.sh points at lib/.
cp -avf "$CURDIR"/libparakeet.so* "$CURDIR/package/lib/" 2>/dev/null || true
cp -avf "$CURDIR"/libparakeet.dylib "$CURDIR/package/lib/" 2>/dev/null || true
if ! ls "$CURDIR"/package/lib/libparakeet.* >/dev/null 2>&1; then
	echo "ERROR: libparakeet shared library not found in $CURDIR, run 'make' first" >&2
	exit 1
fi

# Detect architecture and copy the core runtime libs libparakeet.so links
# against, plus the matching dynamic loader as lib/ld.so.
if [ -f "/lib64/ld-linux-x86-64.so.2" ]; then
    echo "Detected x86_64 architecture, copying x86_64 libraries..."
    cp -arfLv /lib64/ld-linux-x86-64.so.2 "$CURDIR/package/lib/ld.so"
    cp -arfLv /lib/x86_64-linux-gnu/libc.so.6 "$CURDIR/package/lib/libc.so.6"
    cp -arfLv /lib/x86_64-linux-gnu/libgcc_s.so.1 "$CURDIR/package/lib/libgcc_s.so.1"
    cp -arfLv /lib/x86_64-linux-gnu/libstdc++.so.6 "$CURDIR/package/lib/libstdc++.so.6"
    cp -arfLv /lib/x86_64-linux-gnu/libm.so.6 "$CURDIR/package/lib/libm.so.6"
    cp -arfLv /lib/x86_64-linux-gnu/libgomp.so.1 "$CURDIR/package/lib/libgomp.so.1"
    cp -arfLv /lib/x86_64-linux-gnu/libdl.so.2 "$CURDIR/package/lib/libdl.so.2"
    cp -arfLv /lib/x86_64-linux-gnu/librt.so.1 "$CURDIR/package/lib/librt.so.1"
    cp -arfLv /lib/x86_64-linux-gnu/libpthread.so.0 "$CURDIR/package/lib/libpthread.so.0"
elif [ -f "/lib/ld-linux-aarch64.so.1" ]; then
    echo "Detected ARM64 architecture, copying ARM64 libraries..."
    cp -arfLv /lib/ld-linux-aarch64.so.1 "$CURDIR/package/lib/ld.so"
    cp -arfLv /lib/aarch64-linux-gnu/libc.so.6 "$CURDIR/package/lib/libc.so.6"
    cp -arfLv /lib/aarch64-linux-gnu/libgcc_s.so.1 "$CURDIR/package/lib/libgcc_s.so.1"
    cp -arfLv /lib/aarch64-linux-gnu/libstdc++.so.6 "$CURDIR/package/lib/libstdc++.so.6"
    cp -arfLv /lib/aarch64-linux-gnu/libm.so.6 "$CURDIR/package/lib/libm.so.6"
    cp -arfLv /lib/aarch64-linux-gnu/libgomp.so.1 "$CURDIR/package/lib/libgomp.so.1"
    cp -arfLv /lib/aarch64-linux-gnu/libdl.so.2 "$CURDIR/package/lib/libdl.so.2"
    cp -arfLv /lib/aarch64-linux-gnu/librt.so.1 "$CURDIR/package/lib/librt.so.1"
    cp -arfLv /lib/aarch64-linux-gnu/libpthread.so.0 "$CURDIR/package/lib/libpthread.so.0"
elif [ "$(uname -s)" = "Darwin" ]; then
    echo "Detected Darwin — system frameworks linked dynamically, no bundled libs needed"
else
    echo "Error: Could not detect architecture"
    exit 1
fi

# Package GPU libraries (CUDA/ROCm/Intel/Vulkan loader + ICDs + drivers)
# based on BUILD_TYPE so the backend can reach the GPU without the runtime
# base image shipping those drivers.
GPU_LIB_SCRIPT="${REPO_ROOT}/scripts/build/package-gpu-libs.sh"
if [ -f "$GPU_LIB_SCRIPT" ]; then
    echo "Packaging GPU libraries for BUILD_TYPE=${BUILD_TYPE:-cpu}..."
    source "$GPU_LIB_SCRIPT" "$CURDIR/package/lib"
    package_gpu_libs
fi

echo "Packaging completed successfully"
ls -liah "$CURDIR/package/" "$CURDIR/package/lib/"
