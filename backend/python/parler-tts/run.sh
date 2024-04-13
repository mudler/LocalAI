#!/bin/bash

##
## A bash script wrapper that runs the parler-tts server with conda

echo "Launching gRPC server for parler-tts"

export PATH=$PATH:/opt/conda/bin

# Activate conda environment
source activate parler

# get the directory where the bash script is located
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

python $DIR/parler_tts_server.py $@
