#!/bin/bash

# Script to copy the appropriate libraries based on architecture
# This script is used in the final stage of the Dockerfile

set -e

CURDIR=$(dirname "$(realpath $0)")
REPO_ROOT="${CURDIR}/../../.."

# Create lib directory
mkdir -p $CURDIR/package/lib

cp -avf $CURDIR/crispasr $CURDIR/package/
cp -fv $CURDIR/libgocrispasr-*.so $CURDIR/package/ 2>/dev/null || true
cp -fv $CURDIR/libgocrispasr-*.dylib $CURDIR/package/ 2>/dev/null || true
cp -fv $CURDIR/run.sh $CURDIR/package/

# Detect architecture and copy appropriate libraries
source "$CURDIR/../../../scripts/build/package-system-libs.sh" "$CURDIR/package/lib" ""

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
