#!/bin/bash
##
## A bash script wrapper that runs the huggingface server with conda

# Activate conda environment
source activate huggingface

# get the directory where the bash script is located
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

python -m unittest $DIR/test_huggingface.py