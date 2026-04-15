#!/bin/bash
# Script to package runtime shared libraries for the tinygrad backend.
#
# The final Dockerfile.python stage is FROM scratch, so system libraries
# must be explicitly copied into ${BACKEND}/lib so the backend can run on
# any host without installing them. libbackend.sh automatically prepends
# that directory to LD_LIBRARY_PATH at run time.
#
# tinygrad's CPU device (CLANG / LLVM renderer) JIT-compiles kernels at
# runtime. The default `CLANG` path invokes the external `clang` binary via
# subprocess, which does not exist in the scratch image. We force the
# in-process LLVM path (`CPU_LLVM=1` in run.sh) which loads libLLVM.so.*
# through ctypes and bundle the library + its runtime dependencies here.
#
# Also bundle libgomp (pulled by librosa / numpy via numba) and libsndfile
# (required by soundfile -> librosa audio I/O for Whisper).

set -e

CURDIR=$(dirname "$(realpath "$0")")
LIB_DIR="${CURDIR}/lib"
mkdir -p "${LIB_DIR}"

SEARCH_DIRS=(
    /usr/lib/x86_64-linux-gnu
    /usr/lib/aarch64-linux-gnu
    /lib/x86_64-linux-gnu
    /lib/aarch64-linux-gnu
    /usr/lib
    /lib
)

copy_with_symlinks() {
    local soname="$1"
    local hit=""
    for dir in "${SEARCH_DIRS[@]}"; do
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

# tinygrad searches for libLLVM under these sonames (see
# tinygrad/runtime/autogen/llvm.py). Ubuntu 24.04's `llvm` metapackage
# installs `libLLVM-18.so.1` into `/usr/lib/llvm-18/lib/`. Also scan the
# standard lib directories in case a different distro layout puts it in
# /usr/lib/x86_64-linux-gnu.
llvm_so=""
shopt -s nullglob
LLVM_EXTRA_DIRS=(/usr/lib/llvm-*/lib /usr/lib/llvm-*)
# First try the versioned symlink (libLLVM-18.so) since that's what
# tinygrad's DLL loader matches against (see llvm.py DLL name list).
for dir in "${SEARCH_DIRS[@]}" "${LLVM_EXTRA_DIRS[@]}"; do
    for candidate in "${dir}"/libLLVM-[0-9]*.so "${dir}"/libLLVM-[0-9]*.so.[0-9]*; do
        if [ -e "${candidate}" ]; then
            llvm_so="${candidate}"
            break 2
        fi
    done
done
# Fallback: any libLLVM.so file under /usr.
if [ -z "${llvm_so}" ]; then
    llvm_so=$(find /usr -maxdepth 5 -name 'libLLVM*.so*' 2>/dev/null | head -1)
fi
shopt -u nullglob
if [ -z "${llvm_so}" ]; then
    echo "ERROR: libLLVM not found — tinygrad CPU device needs it." >&2
    echo "Install the Ubuntu \`llvm\` package in the builder stage." >&2
    exit 1
fi
echo "Found libLLVM at: ${llvm_so}"
llvm_base=$(basename "${llvm_so}")
real_llvm=$(readlink -f "${llvm_so}")
cp -v "${real_llvm}" "${LIB_DIR}/"
real_base=$(basename "${real_llvm}")
if [ "${real_base}" != "${llvm_base}" ]; then
    ln -sf "${real_base}" "${LIB_DIR}/${llvm_base}"
fi

# libLLVM has soft runtime deps on libedit / libtinfo; pick them up if
# present. They're optional but loading without them can fail.
copy_with_symlinks libedit.so.2
copy_with_symlinks libtinfo.so.6

# Audio I/O for the Whisper path.
copy_with_symlinks libsndfile.so.1
copy_with_symlinks libgomp.so.1

echo "tinygrad packaging completed successfully"
ls -liah "${LIB_DIR}/"
