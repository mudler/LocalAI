#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

PYTHON_VERSION="3.12"
PYTHON_PATCH="12"
PY_STANDALONE_TAG="20251120"

backend_dir=$(dirname "$0")
if [ -d "${backend_dir}/common" ]; then
    source "${backend_dir}/common/libbackend.sh"
else
    source "${backend_dir}/../common/libbackend.sh"
fi

installRequirements
