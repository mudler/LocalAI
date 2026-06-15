#!/bin/bash
set -ex

CURDIR=$(dirname "$(realpath $0)")

export LD_LIBRARY_PATH=$CURDIR/lib:$LD_LIBRARY_PATH
export ONNXRUNTIME_LIB_PATH=$CURDIR/lib/libonnxruntime.so

if [ -f $CURDIR/lib/ld.so ]; then
	echo "Using lib/ld.so"
	exec $CURDIR/lib/ld.so $CURDIR/supertonic "$@"
fi

exec $CURDIR/supertonic "$@"
