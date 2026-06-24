#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

backend_dir=$(dirname "$0")
if [ -d "${backend_dir}/common" ]; then
    source "${backend_dir}/common/libbackend.sh"
else
    source "${backend_dir}/../common/libbackend.sh"
fi

startBackend "$@"
