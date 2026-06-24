#!/bin/bash
set -ex

CURDIR=$(dirname "$(realpath $0)")

if [ "$(uname)" = "Darwin" ]; then
	# macOS uses dyld: there is no ld.so loader, and the search path env
	# var is DYLD_LIBRARY_PATH. ONNX Runtime ships as a .dylib here.
	export DYLD_LIBRARY_PATH=$CURDIR/lib:$DYLD_LIBRARY_PATH
	export ONNXRUNTIME_LIB_PATH=$CURDIR/lib/libonnxruntime.dylib
else
	export LD_LIBRARY_PATH=$CURDIR/lib:$LD_LIBRARY_PATH
	export ONNXRUNTIME_LIB_PATH=$CURDIR/lib/libonnxruntime.so

	if [ -f $CURDIR/lib/ld.so ]; then
		echo "Using lib/ld.so"
		exec $CURDIR/lib/ld.so $CURDIR/supertonic "$@"
	fi
fi

exec $CURDIR/supertonic "$@"
