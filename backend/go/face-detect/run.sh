#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath "$0")")

export LD_LIBRARY_PATH="$CURDIR/lib:$CURDIR:${LD_LIBRARY_PATH:-}"

# If a self-contained ld.so was packaged, route through it so the packaged
# libc / libstdc++ are used instead of the host's (matches the voice-detect /
# whisper / parakeet backends' runtime layout).
if [ -f "$CURDIR/lib/ld.so" ]; then
	echo "Using lib/ld.so"
	exec "$CURDIR/lib/ld.so" "$CURDIR/face-detect-grpc" "$@"
fi

exec "$CURDIR/face-detect-grpc" "$@"
