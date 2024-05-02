#!/bin/bash
##
## A bash script wrapper that runs the bark tests

MY_DIR="$(dirname -- "${BASH_SOURCE[0]}")"

source $MY_DIR/venv/bin/activate

python -m unittest $MY_DIR/test.py