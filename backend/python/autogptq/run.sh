#!/bin/bash

##
## A bash script wrapper that runs the autogptq server

MY_DIR="$(dirname -- "${BASH_SOURCE[0]}")"

source $MY_DIR/venv/bin/activate

python $MY_DIR/autogptq.py $@