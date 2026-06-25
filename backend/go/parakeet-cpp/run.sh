#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath "$0")")

if [ "$(uname)" = "Darwin" ]; then
	export DYLD_LIBRARY_PATH="$CURDIR/lib:"$CURDIR":${DYLD_LIBRARY_PATH:-}"
	export PARAKEET_LIBRARY="$CURDIR/lib/libparakeet.dylib"
else
	export LD_LIBRARY_PATH="$CURDIR/lib:"$CURDIR":${LD_LIBRARY_PATH:-}"
	export PARAKEET_LIBRARY="$CURDIR/lib/libparakeet.so"
fi

# If a self-contained ld.so was packaged, route through it so the
# packaged libc / libstdc++ are used instead of the host's (matches the
# whisper backend's runtime layout). Linux only.
if [ -f "$CURDIR/lib/ld.so" ]; then
	echo "Using lib/ld.so"
	exec "$CURDIR/lib/ld.so" "$CURDIR/parakeet-cpp-grpc" "$@"
fi

exec "$CURDIR/parakeet-cpp-grpc" "$@"
