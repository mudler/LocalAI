#!/bin/bash
# Entry point for the gufo backend image / BACKEND_BINARY mode.
set -e
CURDIR=$(dirname "$(realpath "$0")")
export LD_LIBRARY_PATH="$CURDIR/lib:$LD_LIBRARY_PATH"
# rocBLAS and hipBLASLt find their kernel data next to the bundled libraries.
if [ -d "$CURDIR/lib/rocblas/library" ]; then
    export ROCBLAS_TENSILE_LIBPATH="$CURDIR/lib/rocblas/library"
fi
if [ -d "$CURDIR/lib/hipblaslt/library" ]; then
    export HIPBLASLT_TENSILE_LIBPATH="$CURDIR/lib/hipblaslt/library"
fi
if [ -f "$CURDIR/lib/ld.so" ]; then
    exec "$CURDIR/lib/ld.so" "$CURDIR/grpc-server" "$@"
fi
exec "$CURDIR/grpc-server" "$@"
