#!/bin/bash

# Script to copy the appropriate libraries based on architecture
# This script is used in the final stage of the Dockerfile

set -e

CURDIR=$(dirname "$(realpath $0)")

# Create lib directory
mkdir -p $CURDIR/package/lib

cp -avf $CURDIR/whisper $CURDIR/libgowhisper.so $CURDIR/package/
cp -fv $CURDIR/run.sh $CURDIR/package/

# Detect architecture and copy appropriate libraries
if [ -f "/lib64/ld-linux-x86-64.so.2" ]; then
    # x86_64 architecture
    echo "Detected x86_64 architecture, copying x86_64 libraries..."
    cp -arfLv /lib64/ld-linux-x86-64.so.2 $CURDIR/package/lib/ld.so
    cp -arfLv /lib/x86_64-linux-gnu/libc.so.6 $CURDIR/package/lib/libc.so.6
    cp -arfLv /lib/x86_64-linux-gnu/libgcc_s.so.1 $CURDIR/package/lib/libgcc_s.so.1
    cp -arfLv /lib/x86_64-linux-gnu/libstdc++.so.6 $CURDIR/package/lib/libstdc++.so.6
    cp -arfLv /lib/x86_64-linux-gnu/libm.so.6 $CURDIR/package/lib/libm.so.6
    cp -arfLv /lib/x86_64-linux-gnu/libgomp.so.1 $CURDIR/package/lib/libgomp.so.1
    cp -arfLv /lib/x86_64-linux-gnu/libgcc_s.so.1 $CURDIR/package/lib/libgcc_s.so.1
    cp -arfLv /lib/x86_64-linux-gnu/libstdc++.so.6 $CURDIR/package/lib/libstdc++.so.6
    cp -arfLv /lib/x86_64-linux-gnu/libdl.so.2 $CURDIR/package/lib/libdl.so.2
    cp -arfLv /lib/x86_64-linux-gnu/librt.so.1 $CURDIR/package/lib/librt.so.1
    cp -arfLv /lib/x86_64-linux-gnu/libpthread.so.0 $CURDIR/package/lib/libpthread.so.0
elif [ -f "/lib/ld-linux-aarch64.so.1" ]; then
    # ARM64 architecture
    echo "Detected ARM64 architecture, copying ARM64 libraries..."
    cp -arfLv /lib/ld-linux-aarch64.so.1 $CURDIR/package/lib/ld.so
    cp -arfLv /lib/aarch64-linux-gnu/libc.so.6 $CURDIR/package/lib/libc.so.6
    cp -arfLv /lib/aarch64-linux-gnu/libgcc_s.so.1 $CURDIR/package/lib/libgcc_s.so.1
    cp -arfLv /lib/aarch64-linux-gnu/libstdc++.so.6 $CURDIR/package/lib/libstdc++.so.6
    cp -arfLv /lib/aarch64-linux-gnu/libm.so.6 $CURDIR/package/lib/libm.so.6
    cp -arfLv /lib/aarch64-linux-gnu/libgomp.so.1 $CURDIR/package/lib/libgomp.so.1
    cp -arfLv /lib/aarch64-linux-gnu/libgcc_s.so.1 $CURDIR/package/lib/libgcc_s.so.1
    cp -arfLv /lib/aarch64-linux-gnu/libstdc++.so.6 $CURDIR/package/lib/libstdc++.so.6
    cp -arfLv /lib/aarch64-linux-gnu/libdl.so.2 $CURDIR/package/lib/libdl.so.2
    cp -arfLv /lib/aarch64-linux-gnu/librt.so.1 $CURDIR/package/lib/librt.so.1
    cp -arfLv /lib/aarch64-linux-gnu/libpthread.so.0 $CURDIR/package/lib/libpthread.so.0
elif [ $(uname -s) = "Darwin" ]; then
    echo "Detected Darwin"
else
    echo "Error: Could not detect architecture"
    exit 1
fi

echo "Packaging completed successfully"
ls -liah $CURDIR/package/
ls -liah $CURDIR/package/lib/
