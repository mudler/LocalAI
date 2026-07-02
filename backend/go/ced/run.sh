#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath "$0")")

if [ "$(uname)" = "Darwin" ]; then
	export DYLD_LIBRARY_PATH="$CURDIR/lib:"$CURDIR":${DYLD_LIBRARY_PATH:-}"
	export CED_LIBRARY="$CURDIR/lib/libced.dylib"
else
	export LD_LIBRARY_PATH="$CURDIR/lib:"$CURDIR":${LD_LIBRARY_PATH:-}"
fi

# If a self-contained ld.so was packaged, route through it so the packaged
# libc / libstdc++ are used instead of the host's (matches the sibling backends).
if [ -f "$CURDIR/lib/ld.so" ]; then
	echo "Using lib/ld.so"
	exec "$CURDIR/lib/ld.so" "$CURDIR/ced-grpc" "$@"
fi

exec "$CURDIR/ced-grpc" "$@"
