#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")

mkdir -p $CURDIR/package/lib

cp -avf $CURDIR/opus $CURDIR/package/
cp -avf $CURDIR/run.sh $CURDIR/package/

# The shim extension is OS-specific (.so on Linux, .dylib on macOS).
SHIM_EXT=so
if [ "$(uname)" = "Darwin" ]; then
    SHIM_EXT=dylib
fi

# Copy the opus shim library
cp -avf $CURDIR/libopusshim.$SHIM_EXT $CURDIR/package/lib/

# Copy system libopus so the backend is self-contained: the runtime base
# image has neither libopus-dev (Linux) nor Homebrew (macOS), so codec.go's
# dlopen would otherwise fail. Both name patterns are attempted; only the
# host's matching one exists.
if command -v pkg-config >/dev/null 2>&1 && pkg-config --exists opus; then
    LIBOPUS_DIR=$(pkg-config --variable=libdir opus)
    cp -avf $LIBOPUS_DIR/libopus.so* $CURDIR/package/lib/ 2>/dev/null || true
    cp -avf $LIBOPUS_DIR/libopus*.dylib $CURDIR/package/lib/ 2>/dev/null || true
fi

# Detect architecture and copy appropriate libraries
source "$CURDIR/../../../scripts/build/package-system-libs.sh" "$CURDIR/package/lib" ""

echo "Packaging completed successfully"
ls -liah $CURDIR/package/
ls -liah $CURDIR/package/lib/
