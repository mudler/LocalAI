#!/bin/bash
#
# Bundle avatarforcing-cpp-grpc, libavatarforcing.so, the core runtime libs
# (libc, libstdc++, libgomp and ld.so) and the GPU runtime of the BUILD_TYPE,
# so that the package is self-contained. run.sh starts the binary
# (CGO_ENABLED=0) through lib/ld.so, so the packaged libc is used.

set -e

CURDIR=$(dirname "$(realpath "$0")")
REPO_ROOT="${CURDIR}/../../.."

mkdir -p "$CURDIR/package/lib"

cp -avf "$CURDIR/avatarforcing-cpp-grpc" "$CURDIR/package/"
cp -avf "$CURDIR/run.sh" "$CURDIR/package/"

if [ ! -f "$CURDIR/libavatarforcing.so" ]; then
	echo "ERROR: libavatarforcing.so not found in $CURDIR, run 'make' first" >&2
	exit 1
fi
cp -avf "$CURDIR/libavatarforcing.so" "$CURDIR/package/lib/"

# The core runtime libs that libavatarforcing.so links against, plus the
# matching dynamic loader as lib/ld.so.
source "$REPO_ROOT/scripts/build/package-system-libs.sh" "$CURDIR/package/lib" ""

# The CUDA runtime for a cublas build, so the backend reaches the GPU without
# the runtime base image shipping it.
GPU_LIB_SCRIPT="${REPO_ROOT}/scripts/build/package-gpu-libs.sh"
if [ -f "$GPU_LIB_SCRIPT" ]; then
	echo "Packaging GPU libraries for BUILD_TYPE=${BUILD_TYPE:-cpu}..."
	source "$GPU_LIB_SCRIPT" "$CURDIR/package/lib"
	package_gpu_libs
fi

echo "Packaging completed successfully"
ls -liah "$CURDIR/package/" "$CURDIR/package/lib/"
