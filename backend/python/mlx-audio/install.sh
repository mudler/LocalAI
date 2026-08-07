#!/bin/bash
set -e

USE_PIP=true
PYTHON_VERSION=3.14
PYTHON_PATCH=6
PY_STANDALONE_TAG=20260718

backend_dir=$(dirname $0)

if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

EXTRA_PIP_INSTALL_FLAGS="--no-build-isolation"
installRequirements
"${EDIR}/venv/bin/python" "${MY_DIR}/check_dependencies.py"
