#!/bin/bash
backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

export LD_LIBRARY_PATH="/usr/local/cuda/lib64:/usr/local/cuda/compat:${LD_LIBRARY_PATH:-}"

startBackend $@