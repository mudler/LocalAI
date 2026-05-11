#!/bin/bash
set -e
CURDIR=$(dirname "$(realpath "$0")")
REPO_ROOT="${CURDIR}/../../.."

mkdir -p "$CURDIR/package/lib"
cp -avf "$CURDIR/grpc-server" "$CURDIR/package/"
cp -rfv "$CURDIR/run.sh"     "$CURDIR/package/"

UNAME_S=$(uname -s)
if [ "$UNAME_S" = "Darwin" ]; then
    # Darwin: bundle dylibs via otool -L (handled by scripts/build/ds4-darwin.sh).
    echo "package.sh: Darwin handled by ds4-darwin.sh"
    exit 0
fi

if [ -f "/lib64/ld-linux-x86-64.so.2" ]; then
    cp -arfLv /lib64/ld-linux-x86-64.so.2 "$CURDIR/package/lib/ld.so"
    LIBDIR=/lib/x86_64-linux-gnu
elif [ -f "/lib/ld-linux-aarch64.so.1" ]; then
    cp -arfLv /lib/ld-linux-aarch64.so.1 "$CURDIR/package/lib/ld.so"
    LIBDIR=/lib/aarch64-linux-gnu
else
    echo "package.sh: unknown architecture" >&2; exit 1
fi

for lib in libc.so.6 libgcc_s.so.1 libstdc++.so.6 libm.so.6 libgomp.so.1 \
           libdl.so.2 librt.so.1 libpthread.so.0; do
    cp -arfLv "$LIBDIR/$lib" "$CURDIR/package/lib/$lib"
done

GPU_LIB_SCRIPT="${REPO_ROOT}/scripts/build/package-gpu-libs.sh"
if [ -f "$GPU_LIB_SCRIPT" ]; then
    source "$GPU_LIB_SCRIPT" "$CURDIR/package/lib"
    package_gpu_libs
fi

echo "ds4 package contents:"
ls -lah "$CURDIR/package/" "$CURDIR/package/lib/"
