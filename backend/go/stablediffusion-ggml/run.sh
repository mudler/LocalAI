#!/bin/bash
set -ex

# Get the absolute current dir where the script is located
CURDIR=$(dirname "$(realpath "$0")")

cd /

echo "CPU info:"
if [ "$(uname)" != "Darwin" ]; then
	grep -e "model\sname" /proc/cpuinfo | head -1
	grep -e "flags" /proc/cpuinfo | head -1
fi

if [ "$(uname)" = "Darwin" ]; then
	# macOS: single library variant (Metal or Accelerate). The gosd target is
	# built as a CMake MODULE, which emits a .dylib for a SHARED build but a
	# .so for a MODULE build on Apple, so prefer .dylib and fall back to .so.
	LIBRARY="$CURDIR/libgosd-fallback.dylib"
	if [ ! -e "$LIBRARY" ]; then
		LIBRARY="$CURDIR/libgosd-fallback.so"
	fi
	export DYLD_LIBRARY_PATH="$CURDIR"/lib:$DYLD_LIBRARY_PATH
else
	LIBRARY="$CURDIR/libgosd-fallback.so"

	if grep -q -e "\savx\s" /proc/cpuinfo ; then
		echo "CPU:    AVX    found OK"
		if [ -e "$CURDIR"/libgosd-avx.so ]; then
			LIBRARY="$CURDIR/libgosd-avx.so"
		fi
	fi

	if grep -q -e "\savx2\s" /proc/cpuinfo ; then
		echo "CPU:    AVX2   found OK"
		if [ -e "$CURDIR"/libgosd-avx2.so ]; then
			LIBRARY="$CURDIR/libgosd-avx2.so"
		fi
	fi

	# Check avx 512
	if grep -q -e "\savx512f\s" /proc/cpuinfo ; then
		echo "CPU:    AVX512F found OK"
		if [ -e "$CURDIR"/libgosd-avx512.so ]; then
			LIBRARY="$CURDIR/libgosd-avx512.so"
		fi
	fi

	export LD_LIBRARY_PATH="$CURDIR"/lib:$LD_LIBRARY_PATH
fi

export SD_LIBRARY=$LIBRARY

# If there is a lib/ld.so, use it
if [ -f "$CURDIR"/lib/ld.so ]; then
	echo "Using lib/ld.so"
	echo "Using library: $LIBRARY"
	exec "$CURDIR"/lib/ld.so "$CURDIR"/stablediffusion-ggml "$@"
fi

echo "Using library: $LIBRARY"
exec "$CURDIR"/stablediffusion-ggml "$@"
