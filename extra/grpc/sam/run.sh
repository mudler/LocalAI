#!/bin/bash

##
## A bash script wrapper that runs the sam server with conda
export PATH=$PATH:/opt/conda/bin

# Activate conda environment
source activate sam

# get the directory where the bash script is located
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

python $DIR/sam.py $@