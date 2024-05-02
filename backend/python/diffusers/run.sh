#!/bin/bash

##
## A bash script wrapper that runs the GRPC backend

MY_DIR="$(dirname -- "${BASH_SOURCE[0]}")"

source $MY_DIR/venv/bin/activate

python $MY_DIR/backend_diffusers.py $@