#!/bin/bash
set -e

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

PYTHON_VERSION="3.11"
PYTHON_PATCH="14"
PY_STANDALONE_TAG="20260203"

installRequirements

if [ ! -d ACE-Step-1.5 ]; then
    git clone https://github.com/ace-step/ACE-Step-1.5
    cd ACE-Step-1.5/
    if [ "x${USE_PIP}" == "xtrue" ]; then
        pip install ${EXTRA_PIP_INSTALL_FLAGS:-} --no-deps .
    else
        uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} --no-deps .
    fi
fi

