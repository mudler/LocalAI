#!/bin/bash
set -ex

CURDIR=$(dirname "$(realpath "$0")")

# LocalVQE's runtime CPU-variant loader (ggml_backend_load_all) searches
# get_executable_path() and current_path() — the second one is what saves us
# when /proc/self/exe resolves to lib/ld.so under the bundled-loader path.
# So we cd into "$CURDIR" (where all the libggml-cpu-*.so files live) before
# exec'ing the binary.
cd "$CURDIR"

if [ "$(uname)" = "Darwin" ]; then
	# macOS: LocalVQE is built as a SHARED library, so dyld needs the .dylib +
	# DYLD_LIBRARY_PATH. Prefer .dylib and fall back to .so just in case.
	export DYLD_LIBRARY_PATH="$CURDIR":"$CURDIR"/lib:$DYLD_LIBRARY_PATH
	LOCALVQE_LIBRARY="$CURDIR"/liblocalvqe.dylib
	if [ ! -e "$LOCALVQE_LIBRARY" ]; then
		LOCALVQE_LIBRARY="$CURDIR"/liblocalvqe.so
	fi
	export LOCALVQE_LIBRARY
else
	export LD_LIBRARY_PATH="$CURDIR":"$CURDIR"/lib:$LD_LIBRARY_PATH
	export LOCALVQE_LIBRARY="$CURDIR"/liblocalvqe.so
fi

if [ -f "$CURDIR"/lib/ld.so ]; then
	echo "Using lib/ld.so"
	echo "Using library: $LOCALVQE_LIBRARY"
	exec "$CURDIR"/lib/ld.so "$CURDIR"/localvqe "$@"
fi

echo "Using library: $LOCALVQE_LIBRARY"
exec "$CURDIR"/localvqe "$@"
