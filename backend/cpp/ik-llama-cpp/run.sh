#!/bin/bash
set -ex

# Get the absolute current dir where the script is located
CURDIR=$(dirname "$(realpath $0)")

cd /

echo "CPU info:"
grep -e "model\sname" /proc/cpuinfo | head -1
grep -e "flags" /proc/cpuinfo | head -1

# ik_llama.cpp requires AVX2 — default to avx2 binary
BINARY=ik-llama-cpp-avx2

if [ -e $CURDIR/ik-llama-cpp-fallback ] && ! grep -q -e "\savx2\s" /proc/cpuinfo ; then
	echo "CPU:    AVX2   NOT found, using fallback"
	BINARY=ik-llama-cpp-fallback
fi

# Extend ld library path with the dir where this script is located/lib
if [ "$(uname)" == "Darwin" ]; then
	export DYLD_LIBRARY_PATH=$CURDIR/lib:$DYLD_LIBRARY_PATH
	#export DYLD_FALLBACK_LIBRARY_PATH=$CURDIR/lib:$DYLD_FALLBACK_LIBRARY_PATH
else
	export LD_LIBRARY_PATH=$CURDIR/lib:$LD_LIBRARY_PATH
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
exec $CURDIR/ik-llama-cpp-fallback "$@"
