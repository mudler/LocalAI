#!/bin/bash

# Script to copy the appropriate libraries based on architecture
# This script is used in the final stage of the Dockerfile

set -e

CURDIR=$(dirname "$(realpath $0)")
REPO_ROOT="${CURDIR}/../../.."

# Create lib directory
mkdir -p $CURDIR/package/lib

cp -avf $CURDIR/crispasr $CURDIR/package/
cp -fv $CURDIR/libgocrispasr-*.so $CURDIR/package/
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

# Bundle espeak-ng (+ its libpcaudio/libsonic runtime deps) and its voice data so
# the piper TTS backend can phonemize non-English text. CrispASR dlopens
# libespeak-ng.so.1 at runtime (the MIT-clean path); the dlopen succeeds loading
# libespeak-ng but FAILS if libpcaudio/libsonic are absent, so all three .so are
# required. run.sh points CRISPASR_ESPEAK_DATA_PATH at the bundled data dir.
# Best-effort: only copied when present, so a local dev build without espeak-ng
# installed still packages the rest (English voices keep working).
ESPEAK_LIBDIR=""
for d in /usr/lib/x86_64-linux-gnu /usr/lib/aarch64-linux-gnu; do
    if [ -f "$d/libespeak-ng.so.1" ]; then
        ESPEAK_LIBDIR="$d"
        break
    fi
done
if [ -n "$ESPEAK_LIBDIR" ]; then
    echo "Bundling espeak-ng from $ESPEAK_LIBDIR ..."
    cp -arfLv "$ESPEAK_LIBDIR/libespeak-ng.so.1" $CURDIR/package/lib/
    cp -arfLv "$ESPEAK_LIBDIR/libpcaudio.so.0" $CURDIR/package/lib/
    cp -arfLv "$ESPEAK_LIBDIR/libsonic.so.0" $CURDIR/package/lib/
    if [ -d "$ESPEAK_LIBDIR/espeak-ng-data" ]; then
        cp -arfLv "$ESPEAK_LIBDIR/espeak-ng-data" $CURDIR/package/
    fi
else
    echo "espeak-ng not found; non-English piper voices will not phonemize"
fi

# Package GPU libraries based on BUILD_TYPE
# The GPU library packaging script will detect BUILD_TYPE and copy appropriate GPU libraries
GPU_LIB_SCRIPT="${REPO_ROOT}/scripts/build/package-gpu-libs.sh"
if [ -f "$GPU_LIB_SCRIPT" ]; then
    echo "Packaging GPU libraries for BUILD_TYPE=${BUILD_TYPE:-cpu}..."
    source "$GPU_LIB_SCRIPT" "$CURDIR/package/lib"
    package_gpu_libs
fi

echo "Packaging completed successfully"
ls -liah $CURDIR/package/
ls -liah $CURDIR/package/lib/
