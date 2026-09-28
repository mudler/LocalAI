#!/bin/bash
set -ex

# Get the absolute current dir where the script is located
CURDIR=$(dirname "$(realpath "$0")")

cd /

echo "CPU info:"
grep -e "model\sname" /proc/cpuinfo | head -1
grep -e "flags" /proc/cpuinfo | head -1

BINARY=rocmfp4-fallback

# ROCm-only backend: the accelerator does the compute, so the CPU side is the single
# rocmfp4-fallback build. No cpu-all variant is produced, hence no probing here.

if [ -n "$LLAMACPP_GRPC_SERVERS" ]; then
	if [ -e "$CURDIR"/rocmfp4-grpc ]; then
		BINARY=rocmfp4-grpc
	fi
fi

# Extend ld library path with the dir where this script is located/lib
if [ "$(uname)" == "Darwin" ]; then
	export DYLD_LIBRARY_PATH="$CURDIR"/lib:$DYLD_LIBRARY_PATH
else
	export LD_LIBRARY_PATH="$CURDIR"/lib:$LD_LIBRARY_PATH
	# Tell rocBLAS where to find TensileLibrary data (GPU kernel tuning files)
	if [ -d "$CURDIR/lib/rocblas/library" ]; then
		export ROCBLAS_TENSILE_LIBPATH="$CURDIR"/lib/rocblas/library
	fi
	# Same for hipBLASLt (rocblaslt): the bundled libhipblaslt.so resolves its
	# TensileLibrary_lazy_gfx*.dat kernel data relative to itself, so point it at
	# the bundled data or it falls back to slow generic kernels (issue #10660).
	if [ -d "$CURDIR/lib/hipblaslt/library" ]; then
		export HIPBLASLT_TENSILE_LIBPATH="$CURDIR"/lib/hipblaslt/library
	fi
fi

# If there is a lib/ld.so, use it
if [ -f "$CURDIR"/lib/ld.so ]; then
	echo "Using lib/ld.so"
	echo "Using binary: $BINARY"
	exec "$CURDIR"/lib/ld.so "$CURDIR"/$BINARY "$@"
fi

echo "Using binary: $BINARY"
exec "$CURDIR"/$BINARY "$@"

# We should never reach this point, however just in case we do, run fallback
exec "$CURDIR"/rocmfp4-fallback "$@"
