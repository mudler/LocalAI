#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath "$0")")

if [ "$(uname)" = "Darwin" ]; then
	export DYLD_LIBRARY_PATH="$CURDIR/lib:$CURDIR:${DYLD_LIBRARY_PATH:-}"
	export TIMESTRAL_LIBRARY="$CURDIR/lib/libtimestral.dylib"
else
	export LD_LIBRARY_PATH="$CURDIR/lib:$CURDIR:${LD_LIBRARY_PATH:-}"
	export TIMESTRAL_LIBRARY="$CURDIR/lib/libtimestral.so"
fi

# If a self-contained ld.so was packaged, route through it so the packaged
# libc and libstdc++ are used instead of the host's. Linux only.
if [ -f "$CURDIR/lib/ld.so" ]; then
	echo "Using lib/ld.so"
	exec "$CURDIR/lib/ld.so" "$CURDIR/timestral-cpp-grpc" "$@"
fi

exec "$CURDIR/timestral-cpp-grpc" "$@"
