#!/bin/bash

##
## A bash script wrapper that runs the GRPC backend


if [ -d "/opt/intel" ]; then
    # Assumes we are using the Intel oneAPI container image
    # https://github.com/intel/intel-extension-for-pytorch/issues/538
    export XPU=1
fi

MY_DIR="$(dirname -- "${BASH_SOURCE[0]}")"

source $MY_DIR/venv/bin/activate

python $MY_DIR/transformers_server.py $@