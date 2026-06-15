#!/bin/bash
set -ex

CURDIR=$(dirname "$(realpath $0)")

# LocalVQE's runtime CPU-variant loader (ggml_backend_load_all) searches
# get_executable_path() and current_path() — the second one is what saves us
# when /proc/self/exe resolves to lib/ld.so under the bundled-loader path.
# So we cd into $CURDIR (where all the libggml-cpu-*.so files live) before
# exec'ing the binary.
cd "$CURDIR"

export LD_LIBRARY_PATH=$CURDIR:$CURDIR/lib:$LD_LIBRARY_PATH
export LOCALVQE_LIBRARY=$CURDIR/liblocalvqe.so

if [ -f $CURDIR/lib/ld.so ]; then
	echo "Using lib/ld.so"
	echo "Using library: $LOCALVQE_LIBRARY"
	exec $CURDIR/lib/ld.so $CURDIR/localvqe "$@"
fi

echo "Using library: $LOCALVQE_LIBRARY"
exec $CURDIR/localvqe "$@"
