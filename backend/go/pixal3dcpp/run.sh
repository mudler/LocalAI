#!/bin/bash
set -euo pipefail
CURDIR=$(cd "$(dirname "$0")" && pwd)
if [ "$(uname -s)" = Darwin ]; then
    export DYLD_LIBRARY_PATH="$CURDIR/lib:${DYLD_LIBRARY_PATH:-}"
else
    export LD_LIBRARY_PATH="$CURDIR/lib:${LD_LIBRARY_PATH:-}"
fi
exec "$CURDIR/pixal3dcpp" "$@"
