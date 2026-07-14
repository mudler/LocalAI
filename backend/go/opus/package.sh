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
if [ -f "/lib64/ld-linux-x86-64.so.2" ]; then
    echo "Detected x86_64 architecture, copying x86_64 libraries..."
    cp -arfLv /lib64/ld-linux-x86-64.so.2 $CURDIR/package/lib/ld.so
    cp -arfLv /lib/x86_64-linux-gnu/libc.so.6 $CURDIR/package/lib/libc.so.6
    cp -arfLv /lib/x86_64-linux-gnu/libgcc_s.so.1 $CURDIR/package/lib/libgcc_s.so.1
    cp -arfLv /lib/x86_64-linux-gnu/libstdc++.so.6 $CURDIR/package/lib/libstdc++.so.6
    cp -arfLv /lib/x86_64-linux-gnu/libm.so.6 $CURDIR/package/lib/libm.so.6
    cp -arfLv /lib/x86_64-linux-gnu/libdl.so.2 $CURDIR/package/lib/libdl.so.2
    cp -arfLv /lib/x86_64-linux-gnu/librt.so.1 $CURDIR/package/lib/librt.so.1
    cp -arfLv /lib/x86_64-linux-gnu/libpthread.so.0 $CURDIR/package/lib/libpthread.so.0
elif [ -f "/lib/ld-linux-aarch64.so.1" ]; then
    echo "Detected ARM64 architecture, copying ARM64 libraries..."
    cp -arfLv /lib/ld-linux-aarch64.so.1 $CURDIR/package/lib/ld.so
    cp -arfLv /lib/aarch64-linux-gnu/libc.so.6 $CURDIR/package/lib/libc.so.6
    cp -arfLv /lib/aarch64-linux-gnu/libgcc_s.so.1 $CURDIR/package/lib/libgcc_s.so.1
    cp -arfLv /lib/aarch64-linux-gnu/libstdc++.so.6 $CURDIR/package/lib/libstdc++.so.6
    cp -arfLv /lib/aarch64-linux-gnu/libm.so.6 $CURDIR/package/lib/libm.so.6
    cp -arfLv /lib/aarch64-linux-gnu/libdl.so.2 $CURDIR/package/lib/libdl.so.2
    cp -arfLv /lib/aarch64-linux-gnu/librt.so.1 $CURDIR/package/lib/librt.so.1
    cp -arfLv /lib/aarch64-linux-gnu/libpthread.so.0 $CURDIR/package/lib/libpthread.so.0
elif [ "$(uname -s)" = "Darwin" ]; then
    echo "Detected Darwin — system libraries linked dynamically, no bundled loader needed"
else
    echo "Warning: Could not detect architecture for system library bundling"
fi

echo "Packaging completed successfully"
ls -liah $CURDIR/package/
ls -liah $CURDIR/package/lib/
