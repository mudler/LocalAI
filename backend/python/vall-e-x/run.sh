#!/bin/bash

##
## A bash script wrapper that runs the ttsvalle server with conda
export PATH=$PATH:/opt/conda/bin

# Activate conda environment
source activate ttsvalle

# get the directory where the bash script is located
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

python $DIR/ttvalle.py $@