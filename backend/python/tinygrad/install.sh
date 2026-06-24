#!/bin/bash
set -e

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

# tinygrad >= 0.12 requires Python >= 3.11 (pyproject: `requires-python = ">=3.11"`).
# LocalAI's default portable python is 3.10, so we pin to 3.11.x here.
PYTHON_VERSION="3.11"
PYTHON_PATCH="14"
PY_STANDALONE_TAG="20260203"

installRequirements
