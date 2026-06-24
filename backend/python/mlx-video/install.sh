#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

USE_PIP=true
PYTHON_VERSION="3.11"
PYTHON_PATCH="13"
backend_dir=$(dirname "$0")
if [ -d "${backend_dir}/common" ]; then
    source "${backend_dir}/common/libbackend.sh"
else
    source "${backend_dir}/../common/libbackend.sh"
fi
installRequirements
