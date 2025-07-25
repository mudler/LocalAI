#!/bin/bash
set -ex

# Get the absolute current dir where the script is located
CURDIR=$(dirname "$(realpath $0)")

cd /

BINARY=llama-cpp

## P2P/GRPC mode
if [ -n "$LLAMACPP_GRPC_SERVERS" ]; then
	if [ -e $CURDIR/llama-cpp-grpc ]; then
		BINARY=llama-cpp-grpc
	fi
fi
 
# Extend ld library path with the dir where this script is located/lib
if [ "$(uname)" == "Darwin" ]; then
	DYLD_FALLBACK_LIBRARY_PATH=$CURDIR/lib:$DYLD_FALLBACK_LIBRARY_PATH
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
