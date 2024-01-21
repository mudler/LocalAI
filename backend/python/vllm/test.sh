#!/bin/bash
##
## A bash script wrapper that runs the transformers server with conda

# Activate conda environment
source activate transformers

# get the directory where the bash script is located
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

python -m unittest $DIR/test_backend_vllm.py