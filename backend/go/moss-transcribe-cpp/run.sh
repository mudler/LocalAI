#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath "$0")")

if [ "$(uname)" = "Darwin" ]; then
	export DYLD_LIBRARY_PATH="$CURDIR/lib:"$CURDIR":${DYLD_LIBRARY_PATH:-}"
	export MOSS_TRANSCRIBE_LIBRARY="$CURDIR/lib/libmoss-transcribe.dylib"
else
	export LD_LIBRARY_PATH="$CURDIR/lib:"$CURDIR":${LD_LIBRARY_PATH:-}"
	export MOSS_TRANSCRIBE_LIBRARY="$CURDIR/lib/libmoss-transcribe.so"
fi

# If a self-contained ld.so was packaged, route through it so the packaged libc
# / libstdc++ are used instead of the host's (matches the whisper / parakeet-cpp
# backends' runtime layout). Linux only.
if [ -f "$CURDIR/lib/ld.so" ]; then
	echo "Using lib/ld.so"
	exec "$CURDIR/lib/ld.so" "$CURDIR/moss-transcribe-cpp-grpc" "$@"
fi

exec "$CURDIR/moss-transcribe-cpp-grpc" "$@"
