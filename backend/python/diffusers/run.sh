#!/bin/bash

##
## A bash script wrapper that runs the diffusers server with conda

export PATH=$PATH:/opt/conda/bin

# Activate conda environment
source activate diffusers

if [ -d "/opt/intel" ]; then
    source /opt/intel/oneapi/compiler/latest/env/vars.sh
    source /opt/intel/oneapi/mkl/latest/env/vars.sh
fi

# get the directory where the bash script is located
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

python $DIR/backend_diffusers.py $@
