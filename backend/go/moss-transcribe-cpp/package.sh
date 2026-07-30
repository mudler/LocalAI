#!/bin/bash
#
# Bundle the moss-transcribe-cpp-grpc binary, libmoss-transcribe.so, the core
# runtime libs (libc/libstdc++/libgomp + ld.so) and the GPU runtime for the
# active BUILD_TYPE so the package is self-contained. Mirrors
# backend/go/parakeet-cpp/package.sh; run.sh routes the (CGO_ENABLED=0) binary
# through lib/ld.so so the packaged libc is used instead of the host's.

set -e

CURDIR=$(dirname "$(realpath "$0")")
REPO_ROOT="${CURDIR}/../../.."

mkdir -p "$CURDIR/package/lib"

cp -avf "$CURDIR/moss-transcribe-cpp-grpc" "$CURDIR/package/"
cp -avf "$CURDIR/run.sh" "$CURDIR/package/"

# libmoss-transcribe shared lib + any soname symlinks. On Linux this is
# libmoss-transcribe.so[.X.Y]; on macOS it is libmoss-transcribe.dylib.
# purego.Dlopen resolves it via the *_LIBRARY_PATH that run.sh points at lib/.
cp -avf "$CURDIR"/libmoss-transcribe.so* "$CURDIR/package/lib/" 2>/dev/null || true
cp -avf "$CURDIR"/libmoss-transcribe.dylib "$CURDIR/package/lib/" 2>/dev/null || true
if ! ls "$CURDIR"/package/lib/libmoss-transcribe.* >/dev/null 2>&1; then
	echo "ERROR: libmoss-transcribe shared library not found in $CURDIR, run 'make' first" >&2
	exit 1
fi

# Detect architecture and copy the core runtime libs libmoss-transcribe.so links
# against, plus the matching dynamic loader as lib/ld.so.
source "$CURDIR/../../../scripts/build/package-system-libs.sh" "$CURDIR/package/lib" ""

# Package GPU libraries (CUDA/ROCm/Intel/Vulkan loader + ICDs + drivers) based
# on BUILD_TYPE so the backend can reach the GPU without the runtime base image
# shipping those drivers.
GPU_LIB_SCRIPT="${REPO_ROOT}/scripts/build/package-gpu-libs.sh"
if [ -f "$GPU_LIB_SCRIPT" ]; then
    echo "Packaging GPU libraries for BUILD_TYPE=${BUILD_TYPE:-cpu}..."
    source "$GPU_LIB_SCRIPT" "$CURDIR/package/lib"
    package_gpu_libs
fi

echo "Packaging completed successfully"
ls -liah "$CURDIR/package/" "$CURDIR/package/lib/"
