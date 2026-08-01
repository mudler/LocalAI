#!/bin/bash
set -e

EXTRA_PIP_INSTALL_FLAGS="--no-build-isolation"

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

# CUDA 13 has no prebuilt FlashAttention wheel, so the fallback source build
# exceeds the CI runner's memory when ninja compiles multiple units at once.
if [ "x${BUILD_PROFILE}" = "xcublas13" ]; then
    export MAX_JOBS="${MAX_JOBS:-1}"
    export NVCC_THREADS="${NVCC_THREADS:-1}"
fi

installRequirements
