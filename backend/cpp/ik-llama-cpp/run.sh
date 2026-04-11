#!/bin/bash
set -ex

# Get the absolute current dir where the script is located
CURDIR=$(dirname "$(realpath $0)")

cd /

echo "CPU info:"
grep -e "model\sname" /proc/cpuinfo | head -1
grep -e "flags" /proc/cpuinfo | head -1

BINARY=ik-llama-cpp-fallback

if grep -q -e "\savx\s" /proc/cpuinfo ; then
	echo "CPU:    AVX    found OK"
	if [ -e $CURDIR/ik-llama-cpp-avx ]; then
		BINARY=ik-llama-cpp-avx
	fi
fi

if grep -q -e "\savx2\s" /proc/cpuinfo ; then
	echo "CPU:    AVX2   found OK"
	if [ -e $CURDIR/ik-llama-cpp-avx2 ]; then
		BINARY=ik-llama-cpp-avx2
	fi
fi

# Check avx 512
if grep -q -e "\savx512f\s" /proc/cpuinfo ; then
	echo "CPU:    AVX512F found OK"
	if [ -e $CURDIR/ik-llama-cpp-avx512 ]; then
		BINARY=ik-llama-cpp-avx512
	fi
fi

if [ -n "$LLAMACPP_GRPC_SERVERS" ]; then
	if [ -e $CURDIR/ik-llama-cpp-grpc ]; then
		BINARY=ik-llama-cpp-grpc
	fi
fi

# Extend ld library path with the dir where this script is located/lib
if [ "$(uname)" == "Darwin" ]; then
	export DYLD_LIBRARY_PATH=$CURDIR/lib:$DYLD_LIBRARY_PATH
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
