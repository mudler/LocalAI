#!/bin/bash
#
# T1 packaging stub: copy the binary, run.sh and libdllm.so into package/.
# The full ldd walk (libc, libstdc++, libgomp, GPU runtimes, arch
# detection) lands with the registration task, mirroring
# backend/go/whisper/package.sh.

set -e

CURDIR=$(dirname "$(realpath "$0")")

mkdir -p "$CURDIR/package/lib"

cp -avf "$CURDIR/dllm-grpc" "$CURDIR/package/"
cp -avf "$CURDIR/run.sh" "$CURDIR/package/"

# libdllm.so + any soname symlinks, should upstream ever add them.
cp -avf "$CURDIR"/libdllm.so* "$CURDIR/package/lib/" 2>/dev/null || {
	echo "ERROR: libdllm.so not found in $CURDIR, run 'make' first" >&2
	exit 1
}

echo "T1 package layout (full ldd walk lands with registration):"
ls -liah "$CURDIR/package/" "$CURDIR/package/lib/"
