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

# Each variant directory bundles libtrellis2 plus its libggml* set (the ggml
# sonames collide across SIMD variants, so they can't share one directory).
VARIANT=fallback

if [ "$(uname)" = "Darwin" ]; then
	LIBRARY="$CURDIR/variants/$VARIANT/libtrellis2.dylib"
	if [ ! -e "$LIBRARY" ]; then
		LIBRARY="$CURDIR/variants/$VARIANT/libtrellis2.so"
	fi
	export DYLD_LIBRARY_PATH="$CURDIR/variants/$VARIANT:$CURDIR/lib:$DYLD_LIBRARY_PATH"
else
	if grep -q -e "\savx\s" /proc/cpuinfo ; then
		echo "CPU:    AVX    found OK"
		if [ -d "$CURDIR/variants/avx" ]; then
			VARIANT=avx
		fi
	fi

	if grep -q -e "\savx2\s" /proc/cpuinfo ; then
		echo "CPU:    AVX2   found OK"
		if [ -d "$CURDIR/variants/avx2" ]; then
			VARIANT=avx2
		fi
	fi

	if grep -q -e "\savx512f\s" /proc/cpuinfo ; then
		echo "CPU:    AVX512F found OK"
		if [ -d "$CURDIR/variants/avx512" ]; then
			VARIANT=avx512
		fi
	fi

	LIBRARY="$CURDIR/variants/$VARIANT/libtrellis2.so"
	export LD_LIBRARY_PATH="$CURDIR/variants/$VARIANT:$CURDIR/lib:$LD_LIBRARY_PATH"
fi

export TRELLIS2_LIBRARY=$LIBRARY

# If there is a lib/ld.so, use it
if [ -f "$CURDIR"/lib/ld.so ]; then
	echo "Using lib/ld.so"
	echo "Using library: $LIBRARY"
	exec "$CURDIR"/lib/ld.so "$CURDIR"/trellis2cpp "$@"
fi

echo "Using library: $LIBRARY"
exec "$CURDIR"/trellis2cpp "$@"
