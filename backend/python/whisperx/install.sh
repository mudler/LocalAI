#!/bin/bash
set -e

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

if [ "x${BUILD_PROFILE}" == "xl4t13" ]; then
  PYTHON_VERSION="3.12"
  PYTHON_PATCH="12"
  PY_STANDALONE_TAG="20251120"
fi

if [ "x${BUILD_PROFILE}" == "xl4t12" ]; then
    USE_PIP=true
fi

# --index-strategy is a uv-only flag; skip it when using pip
if [ "x${USE_PIP}" != "xtrue" ]; then
    if [ "x${BUILD_PROFILE}" != "xmetal" ] && [ "x${BUILD_PROFILE}" != "xmps" ]; then
        EXTRA_PIP_INSTALL_FLAGS+=" --index-strategy unsafe-best-match"
    fi
fi

installRequirements
