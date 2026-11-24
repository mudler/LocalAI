#!/bin/bash
#
# Bundle the timestral-cpp-grpc binary, libtimestral, the core runtime libs
# (libc, libstdc++, libgomp and ld.so) and the licence files, so the package is
# self-contained. The engine runs on the CPU only, so no GPU libraries are
# packaged. run.sh routes the (CGO_ENABLED=0) binary through lib/ld.so so the
# packaged libc is used instead of the host's.

set -e

CURDIR=$(dirname "$(realpath "$0")")

mkdir -p "$CURDIR/package/lib"

cp -avf "$CURDIR/timestral-cpp-grpc" "$CURDIR/package/"
cp -avf "$CURDIR/run.sh" "$CURDIR/package/"

# libtimestral and any soname symlinks. On Linux this is libtimestral.so[.X.Y];
# on macOS it is libtimestral.dylib. run.sh points TIMESTRAL_LIBRARY at lib/.
cp -avf "$CURDIR"/libtimestral.so* "$CURDIR/package/lib/" 2>/dev/null || true
cp -avf "$CURDIR"/libtimestral.dylib "$CURDIR/package/lib/" 2>/dev/null || true
if ! ls "$CURDIR"/package/lib/libtimestral.* >/dev/null 2>&1; then
	echo "ERROR: libtimestral shared library not found in $CURDIR, run 'make' first" >&2
	exit 1
fi

# The engine is MIT. Its model code follows an Apache-2.0 reference
# implementation, and that licence asks for the notice to travel with copies.
for f in LICENSE NOTICE; do
	if [ -f "$CURDIR/sources/timestral.cpp/$f" ]; then
		cp -avf "$CURDIR/sources/timestral.cpp/$f" "$CURDIR/package/timestral.cpp-$f"
	fi
done

# Copy the core runtime libs libtimestral links against, plus the matching
# dynamic loader as lib/ld.so.
source "$CURDIR/../../../scripts/build/package-system-libs.sh" "$CURDIR/package/lib" ""

echo "Packaging completed successfully"
ls -liah "$CURDIR/package/" "$CURDIR/package/lib/"
