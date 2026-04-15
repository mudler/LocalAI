#!/bin/bash
backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

# tinygrad's CPU device defaults to the ClangJITRenderer which shells out to
# the `clang` binary. The scratch backend image does not ship clang, so we
# force the in-process LLVM path instead — it loads libLLVM via ctypes from
# the libs bundled by package.sh into ${EDIR}/lib.
export CPU_LLVM=1

# Point tinygrad at the bundled libLLVM explicitly. The DLL loader honors
# `<NAME>_PATH` before scanning system directories.
if [ -z "${LLVM_PATH:-}" ]; then
    for candidate in "${EDIR}"/lib/libLLVM-*.so "${EDIR}"/lib/libLLVM-*.so.* "${EDIR}"/lib/libLLVM.so.*; do
        if [ -e "${candidate}" ]; then
            export LLVM_PATH="${candidate}"
            break
        fi
    done
fi

startBackend $@
