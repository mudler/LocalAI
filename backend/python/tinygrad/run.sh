#!/bin/bash
backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

# tinygrad binds its compute device at import time from a single env var
# (CUDA / HIP / METAL / CLANG). We pick one here based on what driver
# libraries the host has injected into the container — when a user runs
# the image with `--gpus all` (or the equivalent rocm runtime), the
# nvidia-container-toolkit / rocm runtime mounts the right libraries
# under /usr/lib so we can detect them.
#
# tinygrad's CUDA path uses two compiler pairs: an NVRTC-backed one and
# an in-process PTX renderer. We force the PTX renderer here
# (`CUDA_PTX=1`) so the image is independent of the host CUDA toolkit
# version — only libcuda.so.1 (the driver) is required.
find_lib() {
    local soname="$1"
    for dir in /usr/lib/x86_64-linux-gnu /usr/lib64 /usr/lib /lib/x86_64-linux-gnu /lib64 /lib; do
        if [ -e "${dir}/${soname}" ]; then
            echo "${dir}/${soname}"
            return 0
        fi
    done
    return 1
}

if [ -z "${CUDA:-}${HIP:-}${METAL:-}${CLANG:-}" ]; then
    if find_lib libcuda.so.1 >/dev/null; then
        export CUDA=1
        export CUDA_PTX=1
    elif find_lib libamdhip64.so >/dev/null || find_lib libamdhip64.so.6 >/dev/null; then
        export HIP=1
    else
        export CLANG=1
    fi
fi

# The CPU path (CLANG=1) JIT-compiles via libLLVM. Force tinygrad's
# in-process LLVM compiler so we don't need an external `clang` binary
# (which is not present in the scratch image).
export CPU_LLVM=1
if [ -z "${LLVM_PATH:-}" ]; then
    for candidate in "${EDIR}"/lib/libLLVM-*.so "${EDIR}"/lib/libLLVM-*.so.* "${EDIR}"/lib/libLLVM.so.*; do
        if [ -e "${candidate}" ]; then
            export LLVM_PATH="${candidate}"
            break
        fi
    done
fi

startBackend $@
