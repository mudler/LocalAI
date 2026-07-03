#!/bin/bash
set -ex

# Get the absolute current dir where the script is located
CURDIR=$(dirname "$(realpath "$0")")

cd /

echo "CPU info:"
grep -e "model\sname" /proc/cpuinfo | head -1
grep -e "flags" /proc/cpuinfo | head -1

BINARY=llama-cpp-fallback

# CPU images (x86, arm64, darwin) ship a single llama-cpp-cpu-all built with ggml
# CPU_ALL_VARIANTS: ggml's backend registry dlopens the best libggml-cpu-*.so for this
# host, so no shell-side AVX probing. GPU images (cublas/sycl/vulkan/hipblas) ship only
# llama-cpp-fallback (the accelerator does the compute), so fall back to it when absent.
if [ -e "$CURDIR"/llama-cpp-cpu-all ]; then
	BINARY=llama-cpp-cpu-all
fi

if [ -n "$LLAMACPP_GRPC_SERVERS" ]; then
	if [ -e "$CURDIR"/llama-cpp-grpc ]; then
		BINARY=llama-cpp-grpc
	fi
fi
 
# Extend ld library path with the dir where this script is located/lib
if [ "$(uname)" == "Darwin" ]; then
	export DYLD_LIBRARY_PATH="$CURDIR"/lib:$DYLD_LIBRARY_PATH
	#export DYLD_FALLBACK_LIBRARY_PATH="$CURDIR"/lib:$DYLD_FALLBACK_LIBRARY_PATH
else
	export LD_LIBRARY_PATH="$CURDIR"/lib:$LD_LIBRARY_PATH
	# Tell rocBLAS where to find TensileLibrary data (GPU kernel tuning files)
	if [ -d "$CURDIR/lib/rocblas/library" ]; then
		export ROCBLAS_TENSILE_LIBPATH="$CURDIR"/lib/rocblas/library
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
exec "$CURDIR"/llama-cpp-fallback "$@"