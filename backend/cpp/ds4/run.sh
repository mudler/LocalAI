#!/bin/bash
# Entry point for the ds4 backend image / BACKEND_BINARY mode.
set -e
CURDIR=$(dirname "$(realpath "$0")")
export LD_LIBRARY_PATH="$CURDIR/lib:$LD_LIBRARY_PATH"
if [ -f "$CURDIR/lib/ld.so" ]; then
    exec "$CURDIR/lib/ld.so" "$CURDIR/grpc-server" "$@"
fi
exec "$CURDIR/grpc-server" "$@"
