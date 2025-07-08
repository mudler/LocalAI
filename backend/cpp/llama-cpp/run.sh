#!/bin/bash
set -ex

cd /

echo "CPU info:"
grep -e "model\sname" /proc/cpuinfo | head -1
grep -e "flags" /proc/cpuinfo | head -1

BINARY=llama-cpp-fallback

if grep -q -e "\savx\s" /proc/cpuinfo ; then
	echo "CPU:    AVX    found OK"
	BINARY=llama-cpp-avx
else
	echo "CPU: no AVX    found"
	BINARY=llama-cpp-fallback
fi

if grep -q -e "\savx2\s" /proc/cpuinfo ; then
	echo "CPU:    AVX2   found OK"
	BINARY=llama-cpp-avx2
else
	echo "CPU: no AVX2   found"
	BINARY=llama-cpp-fallback
fi

# Check avx 512
if grep -q -e "\savx512\s" /proc/cpuinfo ; then
	echo "CPU:    AVX512 found OK"
	BINARY=llama-cpp-avx512
else
	echo "CPU: no AVX512 found"
	BINARY=llama-cpp-fallback
fi

if [ -n "$LLAMACPP_GRPC_SERVERS" ]; then
	BINARY=llama-cpp-grpc
fi

# Extend ld library path with the dir where this script is located/lib
CURDIR=$(dirname "$0")
if [ "$(uname)" == "Darwin" ]; then
	DYLD_FALLBACK_LIBRARY_PATH=$CURDIR/lib:$DYLD_FALLBACK_LIBRARY_PATH
else
	LD_LIBRARY_PATH=$CURDIR/lib:$LD_LIBRARY_PATH
fi

# If there is a lib/ld.so, use it
if [ -f $CURDIR/lib/ld.so ]; then
	BINARY="$CURDIR/lib/ld.so $BINARY"
fi

echo "Using binary: $BINARY"
exec ./$BINARY "$@"
