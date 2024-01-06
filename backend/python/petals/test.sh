#!/bin/bash
##
## A bash script wrapper that runs the transformers server with conda

# Activate conda environment
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

python -m unittest $DIR/test_petals.py