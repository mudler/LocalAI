#!/bin/bash

##
## A bash script wrapper that runs the exllama server with conda

export PATH=$PATH:/opt/conda/bin

# Activate conda environment
# if source is available use it, or use conda
#
if [ -f /opt/conda/bin/activate ]; then
    source activate petals
else
    eval "$(conda shell.bash hook)"
    conda activate petals
fi

# get the directory where the bash script is located
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

python $DIR/backend_petals.py $@
