#!/bin/bash
set -ex

CURDIR=$(dirname "$(realpath $0)")

cd /

echo "CPU info:"
if [ "$(uname)" != "Darwin" ]; then
	grep -e "model\sname" /proc/cpuinfo | head -1
	grep -e "flags" /proc/cpuinfo | head -1
fi

LIBRARY="$CURDIR/libgovibevoicecpp-fallback.so"

if [ "$(uname)" != "Darwin" ]; then
	if grep -q -e "\savx\s" /proc/cpuinfo ; then
		echo "CPU:    AVX    found OK"
		if [ -e $CURDIR/libgovibevoicecpp-avx.so ]; then
			LIBRARY="$CURDIR/libgovibevoicecpp-avx.so"
		fi
	fi

	if grep -q -e "\savx2\s" /proc/cpuinfo ; then
		echo "CPU:    AVX2   found OK"
		if [ -e $CURDIR/libgovibevoicecpp-avx2.so ]; then
			LIBRARY="$CURDIR/libgovibevoicecpp-avx2.so"
		fi
	fi

	if grep -q -e "\savx512f\s" /proc/cpuinfo ; then
		echo "CPU:    AVX512F found OK"
		if [ -e $CURDIR/libgovibevoicecpp-avx512.so ]; then
			LIBRARY="$CURDIR/libgovibevoicecpp-avx512.so"
		fi
	fi
fi

export LD_LIBRARY_PATH=$CURDIR/lib:$LD_LIBRARY_PATH
export VIBEVOICECPP_LIBRARY=$LIBRARY

if [ -f $CURDIR/lib/ld.so ]; then
	echo "Using lib/ld.so"
	echo "Using library: $LIBRARY"
	exec $CURDIR/lib/ld.so $CURDIR/vibevoice-cpp "$@"
fi

echo "Using library: $LIBRARY"
exec $CURDIR/vibevoice-cpp "$@"
