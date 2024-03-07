#!/bin/bash

##
## A bash script wrapper that runs the transformers server with conda


if [ -d "/opt/intel" ]; then
    # Assumes we are using the Intel oneAPI container image
    # https://github.com/intel/intel-extension-for-pytorch/issues/538
    export XPU=1
else
    export PATH=$PATH:/opt/conda/bin
    # Activate conda environment
    source activate transformers
fi

# get the directory where the bash script is located
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

python $DIR/transformers_server.py $@
