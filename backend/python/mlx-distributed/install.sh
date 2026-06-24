#!/bin/bash
set -e

USE_PIP=true
PYTHON_VERSION=""

backend_dir=$(dirname $0)

if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

installRequirements
