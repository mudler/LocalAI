#!/bin/sh
# SPDX-License-Identifier: MIT
# Use an isolated source and build directory; never downloads or patches source.
set -eu
if [ "$#" -ne 2 ]; then
    echo "Usage: $0 PINNED_LLAMA_SOURCE SCRATCH_BUILD_DIR" >&2
    exit 2
fi
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
src=$(CDPATH= cd -- "$1" && pwd)
build=$2
cmake -S "$here/native-strict" -B "$build" -DLLAMA_SOURCE_DIR="$src" \
    -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF \
    -DLLAMA_BUILD_COMMON=ON -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_TOOLS=OFF \
    -DLLAMA_BUILD_EXAMPLES=OFF -DLLAMA_BUILD_SERVER=OFF -DLLAMA_BUILD_APP=OFF \
    -DLLAMA_OPENSSL=OFF -DGGML_CUDA=OFF -DGGML_VULKAN=OFF -DGGML_METAL=OFF \
    -DGGML_SYCL=OFF -DGGML_OPENCL=OFF -DGGML_BLAS=OFF -DGGML_NATIVE=OFF \
    -DGGML_OPENMP=OFF -DLLAMA_BUILD_COMMIT=c0fd78626405c6d9b934efef0ec7291fcc20e3d6
cmake --build "$build" --target native-strict -j "${JOBS:-4}"
ctest --test-dir "$build" --output-on-failure -R '^native-strict$'
