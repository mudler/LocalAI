#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath "$0")")

export LD_LIBRARY_PATH="$CURDIR/lib:$CURDIR:${LD_LIBRARY_PATH:-}"
export AVATARFORCING_LIBRARY="$CURDIR/lib/libavatarforcing.so"

# Use the packaged ld.so, so the packaged libc and libstdc++ are used instead
# of the host's.
if [ -f "$CURDIR/lib/ld.so" ]; then
	echo "Using lib/ld.so"
	exec "$CURDIR/lib/ld.so" "$CURDIR/avatarforcing-cpp-grpc" "$@"
fi

exec "$CURDIR/avatarforcing-cpp-grpc" "$@"
