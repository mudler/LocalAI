#!/bin/bash
set -ex

# Get the absolute current dir where the script is located
CURDIR=$(dirname "$(realpath $0)")

cd /

echo "CPU info:"
if [ "$(uname)" != "Darwin" ]; then
	grep -e "model\sname" /proc/cpuinfo | head -1
	grep -e "flags" /proc/cpuinfo | head -1
fi

if [ "$(uname)" = "Darwin" ]; then
	# macOS: single dylib variant (Metal or Accelerate)
	LIBRARY="$CURDIR/libgovoxtral-fallback.dylib"
	export DYLD_LIBRARY_PATH=$CURDIR/lib:$DYLD_LIBRARY_PATH
else
	LIBRARY="$CURDIR/libgovoxtral-fallback.so"

	if grep -q -e "\savx\s" /proc/cpuinfo ; then
		echo "CPU:    AVX    found OK"
		if [ -e $CURDIR/libgovoxtral-avx.so ]; then
			LIBRARY="$CURDIR/libgovoxtral-avx.so"
		fi
	fi

	if grep -q -e "\savx2\s" /proc/cpuinfo ; then
		echo "CPU:    AVX2   found OK"
		if [ -e $CURDIR/libgovoxtral-avx2.so ]; then
			LIBRARY="$CURDIR/libgovoxtral-avx2.so"
		fi
	fi

	export LD_LIBRARY_PATH=$CURDIR/lib:$LD_LIBRARY_PATH
fi

export VOXTRAL_LIBRARY=$LIBRARY

# If there is a lib/ld.so, use it (Linux only)
if [ -f $CURDIR/lib/ld.so ]; then
	echo "Using lib/ld.so"
	echo "Using library: $LIBRARY"
	exec $CURDIR/lib/ld.so $CURDIR/voxtral "$@"
fi

echo "Using library: $LIBRARY"
exec $CURDIR/voxtral "$@"
