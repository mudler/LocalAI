#!/bin/bash
# Entry point for the privacy-filter backend image / BACKEND_BINARY mode.
set -e
CURDIR=$(dirname "$(realpath "$0")")
# macOS has no bundled ld.so; the darwin package ships only dylibs under lib/,
# resolved via DYLD_LIBRARY_PATH (the ld.so branch below is skipped there).
if [ "$(uname)" = "Darwin" ]; then
    export DYLD_LIBRARY_PATH="$CURDIR/lib:$DYLD_LIBRARY_PATH"
else
    export LD_LIBRARY_PATH="$CURDIR/lib:$LD_LIBRARY_PATH"
fi
if [ -f "$CURDIR/lib/ld.so" ]; then
    exec "$CURDIR/lib/ld.so" "$CURDIR/grpc-server" "$@"
fi
exec "$CURDIR/grpc-server" "$@"
