#!/bin/bash
set -ex

# Get the absolute current dir where the script is located
CURDIR=$(dirname "$(realpath $0)")

cd /

echo "CPU info:"
grep -e "model\sname" /proc/cpuinfo | head -1
grep -e "flags" /proc/cpuinfo | head -1

BINARY=turboquant-fallback

# x86/arm64 ship a single turboquant-cpu-all built with ggml CPU_ALL_VARIANTS: ggml's
# backend registry dlopens the best libggml-cpu-*.so for this host, so no shell-side
# probing. ROCm ships only turboquant-fallback, so fall back to it when cpu-all is absent.
if [ -e $CURDIR/turboquant-cpu-all ]; then
	BINARY=turboquant-cpu-all
fi

if [ -n "$LLAMACPP_GRPC_SERVERS" ]; then
	if [ -e $CURDIR/turboquant-grpc ]; then
		BINARY=turboquant-grpc
	fi
fi

# Extend ld library path with the dir where this script is located/lib
if [ "$(uname)" == "Darwin" ]; then
	export DYLD_LIBRARY_PATH=$CURDIR/lib:$DYLD_LIBRARY_PATH
else
	export LD_LIBRARY_PATH=$CURDIR/lib:$LD_LIBRARY_PATH
	# Tell rocBLAS where to find TensileLibrary data (GPU kernel tuning files)
	if [ -d "$CURDIR/lib/rocblas/library" ]; then
		export ROCBLAS_TENSILE_LIBPATH=$CURDIR/lib/rocblas/library
	fi
fi

# If there is a lib/ld.so, use it
if [ -f $CURDIR/lib/ld.so ]; then
	echo "Using lib/ld.so"
	echo "Using binary: $BINARY"
	exec $CURDIR/lib/ld.so $CURDIR/$BINARY "$@"
fi

echo "Using binary: $BINARY"
exec $CURDIR/$BINARY "$@"

# We should never reach this point, however just in case we do, run fallback
exec $CURDIR/turboquant-fallback "$@"
