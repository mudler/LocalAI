#!/bin/bash
set -ex

# Get the absolute current dir where the script is located
CURDIR=$(dirname "$(realpath "$0")")

cd /

echo "CPU info:"
grep -e "model\sname" /proc/cpuinfo | head -1
grep -e "flags" /proc/cpuinfo | head -1

BINARY=turboquant-fallback

# CPU images and most x86 GPU images ship a single turboquant-cpu-all built with ggml
# CPU_ALL_VARIANTS: ggml's
# backend registry dlopens the best libggml-cpu-*.so for this host, so no shell-side
# probing. GPU arm64 images still ship turboquant-fallback until their builder toolchains
# support ggml's complete arm variant matrix, and so do the SYCL images, whose icpx
# compiler hangs on the sapphirerapids variant.
if [ -e "$CURDIR"/turboquant-cpu-all ]; then
	BINARY=turboquant-cpu-all
fi

if [ -n "$LLAMACPP_GRPC_SERVERS" ]; then
	if [ -e "$CURDIR"/turboquant-grpc ]; then
		BINARY=turboquant-grpc
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
	# Backends built for Intel GPUs carry a copy of the Intel graphics driver,
	# and libze_loader is only there in those builds. Level Zero looks for a
	# driver on its own, so point it at the copy that came with this backend: it
	# was built against the same C library, while the machine's own driver may
	# not have been, and loading that one can crash on start.
	#
	# Anything the user set is left alone, so a machine with a graphics card
	# newer than the driver carried here can still be told to use its own.
	# Nothing is said about OpenCL: no OpenCL driver is carried, so anything we
	# set there would leave OpenCL worse off than the machine's own setup.
	if [ -e "$CURDIR/lib/libze_loader.so.1" ]; then
		if [ -e "$CURDIR/lib/libze_intel_gpu.so.1" ] && [ -z "${ZE_ENABLE_ALT_DRIVERS:-}" ]; then
			export ZE_ENABLE_ALT_DRIVERS="$CURDIR"/lib/libze_intel_gpu.so.1
		fi
		# Ask the driver how much graphics memory is free. Without this, the
		# backend reads zero on an integrated graphics chip, because such a chip
		# shares the system memory instead of having its own.
		if [ -z "${ZES_ENABLE_SYSMAN:-}" ]; then
			export ZES_ENABLE_SYSMAN=1
		fi
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
exec "$CURDIR"/turboquant-fallback "$@"
