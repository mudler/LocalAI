#!/bin/bash
source $(dirname $0)/../common/libbackend.sh

if [ -d "/opt/intel" ]; then
    # Assumes we are using the Intel oneAPI container image
    # https://github.com/intel/intel-extension-for-pytorch/issues/538
    export XPU=1
fi

startBackend $@