#!/bin/bash
set -ex

CURDIR=$(dirname "$(realpath $0)")

if [ "$(uname)" = "Darwin" ]; then
	export DYLD_LIBRARY_PATH=$CURDIR/lib:$DYLD_LIBRARY_PATH
	export SHERPA_SHIM_LIBRARY=$CURDIR/lib/libsherpa-shim.dylib
	export SHERPA_ONNX_LIBRARY=$CURDIR/lib/libsherpa-onnx-c-api.dylib
else
	export LD_LIBRARY_PATH=$CURDIR/lib:$LD_LIBRARY_PATH
fi

if [ -f $CURDIR/lib/ld.so ]; then
	echo "Using lib/ld.so"
	exec $CURDIR/lib/ld.so $CURDIR/sherpa-onnx "$@"
fi

exec $CURDIR/sherpa-onnx "$@"
