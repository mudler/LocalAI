#!/bin/bash
set -ex

# Get the absolute current dir where the script is located
CURDIR=$(dirname "$(realpath "$0")")

cd /

# vllm.cpp ships ONE portable library per platform (SIMD tiers are per-file
# with runtime dispatch), so there is no per-CPU variant probing here.
if [ "$(uname)" = "Darwin" ]; then
	LIBRARY="$CURDIR/libvllm.dylib"
	export DYLD_LIBRARY_PATH="$CURDIR"/lib:$DYLD_LIBRARY_PATH
else
	LIBRARY="$CURDIR/libvllm.so"
	export LD_LIBRARY_PATH="$CURDIR"/lib:$LD_LIBRARY_PATH
fi

export VLLM_CPP_LIBRARY=$LIBRARY

# If there is a lib/ld.so, use it
if [ -f "$CURDIR"/lib/ld.so ]; then
	echo "Using lib/ld.so"
	echo "Using library: $LIBRARY"
	exec "$CURDIR"/lib/ld.so "$CURDIR"/vllm-cpp "$@"
fi

echo "Using library: $LIBRARY"
exec "$CURDIR"/vllm-cpp "$@"
