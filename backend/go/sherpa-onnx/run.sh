#!/bin/bash
set -ex

CURDIR=$(dirname "$(realpath $0)")

export LD_LIBRARY_PATH=$CURDIR/lib:$LD_LIBRARY_PATH

if [ -f $CURDIR/lib/ld.so ]; then
	echo "Using lib/ld.so"
	exec $CURDIR/lib/ld.so $CURDIR/sherpa-onnx "$@"
fi

exec $CURDIR/sherpa-onnx "$@"
