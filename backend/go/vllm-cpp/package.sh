#!/bin/bash

# Script to copy the appropriate libraries based on architecture
# This script is used in the final stage of the Dockerfile

set -e

CURDIR=$(dirname "$(realpath $0)")
REPO_ROOT="${CURDIR}/../../.."

# Create lib directory
mkdir -p $CURDIR/package/lib

cp -avf $CURDIR/vllm-cpp $CURDIR/package/
cp -fLv $CURDIR/libvllm.so $CURDIR/package/ 2>/dev/null || true
cp -fLv $CURDIR/libvllm.dylib $CURDIR/package/ 2>/dev/null || true
cp -fv $CURDIR/run.sh $CURDIR/package/

# Detect architecture and copy appropriate libraries
if [ -f "/lib64/ld-linux-x86-64.so.2" ]; then
    # x86_64 architecture
    echo "Detected x86_64 architecture, copying x86_64 libraries..."
    cp -arfLv /lib64/ld-linux-x86-64.so.2 $CURDIR/package/lib/ld.so
    cp -arfLv /lib/x86_64-linux-gnu/libc.so.6 $CURDIR/package/lib/libc.so.6
    cp -arfLv /lib/x86_64-linux-gnu/libgcc_s.so.1 $CURDIR/package/lib/libgcc_s.so.1
    cp -arfLv /lib/x86_64-linux-gnu/libstdc++.so.6 $CURDIR/package/lib/libstdc++.so.6
    cp -arfLv /lib/x86_64-linux-gnu/libm.so.6 $CURDIR/package/lib/libm.so.6
    cp -arfLv /lib/x86_64-linux-gnu/libgomp.so.1 $CURDIR/package/lib/libgomp.so.1
    cp -arfLv /lib/x86_64-linux-gnu/libdl.so.2 $CURDIR/package/lib/libdl.so.2
    cp -arfLv /lib/x86_64-linux-gnu/librt.so.1 $CURDIR/package/lib/librt.so.1
    cp -arfLv /lib/x86_64-linux-gnu/libpthread.so.0 $CURDIR/package/lib/libpthread.so.0
elif [ -f "/lib/ld-linux-aarch64.so.1" ]; then
    # ARM64 architecture
    echo "Detected ARM64 architecture, copying ARM64 libraries..."
    cp -arfLv /lib/ld-linux-aarch64.so.1 $CURDIR/package/lib/ld.so
    cp -arfLv /lib/aarch64-linux-gnu/libc.so.6 $CURDIR/package/lib/libc.so.6
    cp -arfLv /lib/aarch64-linux-gnu/libgcc_s.so.1 $CURDIR/package/lib/libgcc_s.so.1
    cp -arfLv /lib/aarch64-linux-gnu/libstdc++.so.6 $CURDIR/package/lib/libstdc++.so.6
    cp -arfLv /lib/aarch64-linux-gnu/libm.so.6 $CURDIR/package/lib/libm.so.6
    cp -arfLv /lib/aarch64-linux-gnu/libgomp.so.1 $CURDIR/package/lib/libgomp.so.1
    cp -arfLv /lib/aarch64-linux-gnu/libdl.so.2 $CURDIR/package/lib/libdl.so.2
    cp -arfLv /lib/aarch64-linux-gnu/librt.so.1 $CURDIR/package/lib/librt.so.1
    cp -arfLv /lib/aarch64-linux-gnu/libpthread.so.0 $CURDIR/package/lib/libpthread.so.0
elif [ $(uname -s) = "Darwin" ]; then
    echo "Detected Darwin"
    # Vendor the optional MLX GEMM provider, when libvllm was built against it.
    # Three facts drive every line below, each verified on an Apple M4 before it
    # was written:
    #   1. libvllm.dylib carries an LC_LOAD_DYLIB on @rpath/libmlx.dylib, and its
    #      build-time LC_RPATH points inside the build venv. That path does not
    #      exist on a user's machine, so it must become @loader_path/lib.
    #   2. MLX finds its ~100 MB mlx.metallib beside its OWN dylib, so the two
    #      files have to land in the same directory or every Metal op dies with
    #      "Failed to load the default metallib".
    #   3. install_name_tool invalidates the code signature, and macOS refuses to
    #      load an arm64 image whose signature does not match, so the patched
    #      library must be re-signed ad-hoc afterwards.
    if otool -L "$CURDIR/package/libvllm.dylib" 2>/dev/null | grep -q "libmlx.dylib"; then
        MLX_LIB_DIR="${MLX_ROOT}/lib"
        if [ ! -f "$MLX_LIB_DIR/libmlx.dylib" ] || [ ! -f "$MLX_LIB_DIR/mlx.metallib" ]; then
            echo "Error: libvllm.dylib links libmlx.dylib but $MLX_LIB_DIR is missing libmlx.dylib/mlx.metallib" >&2
            exit 1
        fi
        echo "Vendoring the MLX GEMM provider from $MLX_LIB_DIR"
        cp -fLv "$MLX_LIB_DIR/libmlx.dylib" "$CURDIR/package/lib/"
        cp -fLv "$MLX_LIB_DIR/mlx.metallib" "$CURDIR/package/lib/"
        # MLX is MIT and we redistribute its binaries, so its license ships with
        # them. mlx-metal is the wheel carrying the dylib and the metallib.
        MLX_LICENSE=$(ls "${MLX_ROOT}"/../mlx_metal-*.dist-info/licenses/LICENSE 2>/dev/null | head -1)
        if [ -z "$MLX_LICENSE" ]; then
            MLX_LICENSE=$(ls "${MLX_ROOT}"/../mlx-*.dist-info/licenses/LICENSE 2>/dev/null | head -1)
        fi
        if [ -z "$MLX_LICENSE" ]; then
            echo "Error: could not find the MLX LICENSE to redistribute alongside libmlx.dylib" >&2
            exit 1
        fi
        cp -fLv "$MLX_LICENSE" "$CURDIR/package/lib/LICENSE.mlx"
        # Drop every build-tree rpath, then point at the packaged copy.
        otool -l "$CURDIR/package/libvllm.dylib" | awk '/LC_RPATH/{f=1;next} f&&/ path /{print $2;f=0}' | while read -r rp; do
            install_name_tool -delete_rpath "$rp" "$CURDIR/package/libvllm.dylib" 2>/dev/null || true
        done
        install_name_tool -add_rpath "@loader_path/lib" "$CURDIR/package/libvllm.dylib"
        codesign -f -s - "$CURDIR/package/libvllm.dylib"
        # A broken rpath must fail the BUILD, not the user's first inference.
        if ! otool -l "$CURDIR/package/libvllm.dylib" | grep -q "@loader_path/lib"; then
            echo "Error: libvllm.dylib did not get the @loader_path/lib rpath" >&2
            exit 1
        fi
    fi
else
    echo "Error: Could not detect architecture"
    exit 1
fi

# Package GPU libraries based on BUILD_TYPE
GPU_LIB_SCRIPT="${REPO_ROOT}/scripts/build/package-gpu-libs.sh"
if [ -f "$GPU_LIB_SCRIPT" ]; then
    echo "Packaging GPU libraries for BUILD_TYPE=${BUILD_TYPE:-cpu}..."
    source "$GPU_LIB_SCRIPT" "$CURDIR/package/lib"
    package_gpu_libs
fi

echo "Packaging completed successfully"
ls -liah $CURDIR/package/
ls -liah $CURDIR/package/lib/
