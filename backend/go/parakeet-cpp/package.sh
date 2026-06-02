#!/bin/bash
#
# L0 packaging stub: copy the binary, run.sh and libparakeet.so* into
# package/. The full ldd walk (libc, libstdc++, libgomp, GPU runtimes,
# arch detection) lands in L3, mirroring backend/go/whisper/package.sh.

set -e

CURDIR=$(dirname "$(realpath "$0")")

mkdir -p "$CURDIR/package/lib"

cp -avf "$CURDIR/parakeet-cpp-grpc" "$CURDIR/package/"
cp -avf "$CURDIR/run.sh" "$CURDIR/package/"

# libparakeet.so + any soname symlinks (libparakeet.so.X, libparakeet.so.X.Y).
cp -avf "$CURDIR"/libparakeet.so* "$CURDIR/package/lib/" 2>/dev/null || {
	echo "ERROR: libparakeet.so not found in $CURDIR, run 'make' first" >&2
	exit 1
}

echo "L0 package layout (full ldd walk lands in L3):"
ls -liah "$CURDIR/package/" "$CURDIR/package/lib/"
