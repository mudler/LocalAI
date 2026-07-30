#!/bin/bash
# Entry point for the audio-cpp backend image and for BACKEND_BINARY mode.
#
# The image's final stage is FROM scratch, so the package root is / and there is
# no system loader, no system libc and no fallback library path. Everything the
# process opens has to be inside the package, and it has to be findable by the
# two mechanisms that actually do the finding: the dynamic linker, and
# audio.cpp's own directory scans.
set -e

CURDIR=$(dirname "$(realpath "$0")")

if [ "$(uname -s)" = "Darwin" ]; then
	export DYLD_LIBRARY_PATH="$CURDIR/lib:$CURDIR:$DYLD_LIBRARY_PATH"
	exec "$CURDIR/grpc-server" "$@"
fi

# $CURDIR is on the path as well as $CURDIR/lib: the ggml shared objects the
# CPU-all-variants build produces sit in the package root, next to the binary,
# not in lib/. See the comment below for why they cannot live in lib/.
export LD_LIBRARY_PATH="$CURDIR/lib:$CURDIR:$LD_LIBRARY_PATH"

# THE BUNDLED LOADER IS AT THE PACKAGE ROOT, NOT AT lib/ld.so. DO NOT MOVE IT.
#
# Exec'ing the loader is what pins the bundled glibc to the matching ld.so, and
# every other C++ backend here does it. The cost is that /proc/self/exe then
# names the LOADER rather than grpc-server, and this backend has two consumers
# of /proc/self/exe that both have to land on the package root:
#
#   - ggml's backend registry DISCOVERS the per-microarch libggml-cpu-*.so by
#     scanning dirname(/proc/self/exe) and the current directory. Those files
#     are dlopened, never linked, so no library path and no RUNPATH reaches
#     them: they have to be in a directory ggml scans.
#   - resolve_model_path() turns "bundled:<name>" into
#     dirname(/proc/self/exe)/assets/<name>, which is how the bundled
#     silero_vad and marblenet_vad models resolve with nothing downloaded.
#
# backend/cpp/llama-cpp/package.sh answers the first of these by keeping
# lib/ld.so and moving the ggml objects INTO lib/. That does not generalise
# here, because it would also drag assets/ into lib/ to keep the second
# consumer working. Putting the loader in the package root instead makes
# dirname(/proc/self/exe) the package root, so the binary, the ggml objects and
# assets/ all sit in the one directory that all three mechanisms agree on.
#
# The ggml half has a second chance that the bundled: half does not: LocalAI
# sets the backend process cwd to the directory holding run.sh
# (pkg/model/process.go), so ggml's fs::current_path() fallback would find the
# objects in normal operation whatever the loader's placement. That fallback is
# worth little here. It holds only for the launcher that sets that cwd, it is
# gone the moment anyone runs the binary by hand or through a wrapper that
# chdirs, and resolve_model_path has no equivalent, which would leave the only
# zero-download path in this backend resting on it. Rooting the loader is the
# one layout where all three mechanisms agree without depending on the cwd.
if [ -f "$CURDIR/ld.so" ]; then
	exec "$CURDIR/ld.so" "$CURDIR/grpc-server" "$@"
fi

exec "$CURDIR/grpc-server" "$@"
