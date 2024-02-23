#!/bin/bash

##
## A bash script wrapper that runs the diffusers server with conda

export PATH=$PATH:/opt/conda/bin

if [ -d "/opt/intel" ]; then
    # Assumes we are using the Intel oneAPI container image
    #source /opt/intel/oneapi/compiler/latest/env/vars.sh
    #source /opt/intel/oneapi/mkl/latest/env/vars.sh
    export XPU=1
else
    # Activate conda environment
    source activate diffusers
fi

# get the directory where the bash script is located
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

python $DIR/backend_diffusers.py $@
