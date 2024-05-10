#!/bin/bash
##
## A bash script wrapper that runs python unittests

MY_DIR="$(dirname -- "${BASH_SOURCE[0]}")"

source $MY_DIR/venv/bin/activate

if [ -f "${MY_DIR}/test.py" ]; then
    pushd ${MY_DIR}/source
    python -m unittest test.py
    popd
else
    echo "ERROR: No tests defined for backend!"
    exit 1
fi