#!/bin/bash
# Script to package runtime shared libraries for the vllm backend.
#
# The final Dockerfile.python stage is FROM scratch, so system libraries
# must be explicitly copied into ${BACKEND}/lib so the backend can run on
# any host without installing them. libbackend.sh automatically adds that
# directory to LD_LIBRARY_PATH at run time.
#
# vllm's CPU C++ extension (vllm._C) dlopens libnuma.so.1 at import time;
# if it's missing, the _C_utils torch ops are never registered and the
# engine crashes with AttributeError on init_cpu_threads_env. libgomp is
# used by torch's CPU kernels; on some stripped-down hosts it's also
# absent, so we bundle it too.

set -e

CURDIR=$(dirname "$(realpath "$0")")
LIB_DIR="${CURDIR}/lib"
mkdir -p "${LIB_DIR}"

copy_with_symlinks() {
    local soname="$1"
    local hit=""
    for dir in /usr/lib/x86_64-linux-gnu /usr/lib/aarch64-linux-gnu /lib/x86_64-linux-gnu /lib/aarch64-linux-gnu /usr/lib /lib; do
        if [ -e "${dir}/${soname}" ]; then
            hit="${dir}/${soname}"
            break
        fi
    done
    if [ -z "${hit}" ]; then
        echo "warning: ${soname} not found in standard lib paths" >&2
        return 0
    fi
    # Follow the symlink to the real file, copy it, then recreate the symlink.
    local real
    real=$(readlink -f "${hit}")
    cp -v "${real}" "${LIB_DIR}/"
    local real_base
    real_base=$(basename "${real}")
    if [ "${real_base}" != "${soname}" ]; then
        ln -sf "${real_base}" "${LIB_DIR}/${soname}"
    fi
}

copy_with_symlinks libnuma.so.1
copy_with_symlinks libgomp.so.1

echo "vllm packaging completed successfully"
ls -liah "${LIB_DIR}/"
