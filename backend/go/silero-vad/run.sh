#!/bin/bash
set -ex

CURDIR=$(dirname "$(realpath "$0")")

if [ "$(uname)" = "Darwin" ]; then
	export DYLD_LIBRARY_PATH="$CURDIR"/lib:$DYLD_LIBRARY_PATH
else
	export LD_LIBRARY_PATH="$CURDIR"/lib:$LD_LIBRARY_PATH
fi

# If there is a lib/ld.so, use it
if [ -f "$CURDIR"/lib/ld.so ]; then
	echo "Using lib/ld.so"
	exec "$CURDIR"/lib/ld.so "$CURDIR"/silero-vad "$@"
fi

exec "$CURDIR"/silero-vad "$@"