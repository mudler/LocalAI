#!/bin/bash
set -e

backend_dir=$(dirname "$0")
if [ -d "$backend_dir/common" ]; then
    source "$backend_dir/common/libbackend.sh"
else
    source "$backend_dir/../common/libbackend.sh"
fi

PYTHON_VERSION="3.11"
PYTHON_PATCH="13"
installRequirements
