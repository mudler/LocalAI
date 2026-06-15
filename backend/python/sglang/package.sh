#!/bin/bash
# Package runtime shared libraries for the sglang backend.
#
# Dockerfile.python's final stage is FROM scratch — every system library
# the backend dlopens at runtime must be explicitly copied into
# ${BACKEND}/lib, which libbackend.sh adds to LD_LIBRARY_PATH.
#
# sglang's CPU kernel links against libnuma and libtbb; torch's CPU
# kernels use libgomp; tcmalloc + iomp5 are preloaded per sglang's
# docker/xeon.Dockerfile recipe for best CPU throughput. Missing any of
# these makes the engine crash on import.

set -e

CURDIR=$(dirname "$(realpath "$0")")
LIB_DIR="${CURDIR}/lib"
mkdir -p "${LIB_DIR}"

copy_with_symlinks() {
    local soname="$1"
    local hit=""
    for dir in \
        /usr/lib/x86_64-linux-gnu \
        /usr/lib/aarch64-linux-gnu \
        /lib/x86_64-linux-gnu \
        /lib/aarch64-linux-gnu \
        /usr/lib \
        /lib; do
        if [ -e "${dir}/${soname}" ]; then
            hit="${dir}/${soname}"
            break
        fi
    done
    if [ -z "${hit}" ]; then
        echo "warning: ${soname} not found in standard lib paths" >&2
        return 0
    fi
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
copy_with_symlinks libtbb.so.12
copy_with_symlinks libtbbmalloc.so.2
copy_with_symlinks libtcmalloc.so.4

# intel-openmp ships libiomp5.so inside the venv under venv/lib/ — sglang's
# CPU kernel was compiled against its __kmpc_* symbols, so it must be on
# LD_LIBRARY_PATH at runtime. Copy it into the backend lib dir where
# libbackend.sh will pick it up.
if [ -f "${CURDIR}/venv/lib/libiomp5.so" ]; then
    cp -v "${CURDIR}/venv/lib/libiomp5.so" "${LIB_DIR}/"
fi

echo "sglang packaging completed successfully"
ls -liah "${LIB_DIR}/"
