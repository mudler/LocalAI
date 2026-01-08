#!/bin/bash
set -ex

CURDIR=$(dirname "$(realpath $0)")
export LD_LIBRARY_PATH=$CURDIR/lib:$LD_LIBRARY_PATH

# If there is a lib/ld.so, use it
if [ -f $CURDIR/lib/ld.so ]; then
	echo "Using lib/ld.so"
	exec $CURDIR/lib/ld.so $CURDIR/bark-cpp "$@"
fi

exec $CURDIR/bark-cpp "$@"