#!/bin/bash

##
## A bash script wrapper that runs the exllama server with conda

export PATH=$PATH:/opt/conda/bin

CONDA_ENV=petals

# Activate conda environment
# if source is available use it, or use conda
#
if [ -f /opt/conda/bin/activate ]; then
    source activate $CONDA_ENV
else
    eval "$(conda shell.bash hook)"
    conda activate $CONDA_ENV
fi

# get the directory where the bash script is located
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

python $DIR/backend_petals.py $@
