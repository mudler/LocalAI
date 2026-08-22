#!/bin/bash
backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

cuda_home=${CUDA_HOME:-/usr/local/cuda}
if [ -z "${TRITON_PTXAS_PATH:-}" ] && [ -x "$cuda_home/bin/ptxas" ]; then
    export TRITON_PTXAS_PATH="$cuda_home/bin/ptxas"
fi

startBackend $@
