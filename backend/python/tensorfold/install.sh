#!/bin/bash
set -e

backend_dir=$(dirname $0)

if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

# TensorFold requires Python 3.11 or newer; libbackend defaults to 3.10.
PYTHON_VERSION="3.12"
PYTHON_PATCH="12"
PY_STANDALONE_TAG="20251120"

# The cu130 torch wheels live on a dedicated index, and uv's per-package
# first-match strategy would pick the PyPI (CUDA 12) wheel.
if [ "x${BUILD_PROFILE}" == "xcublas13" ] || [ "x${BUILD_PROFILE}" == "xl4t13" ]; then
    EXTRA_PIP_INSTALL_FLAGS+=" --index-strategy=unsafe-best-match"
fi

installRequirements

# Installed from the pinned checkout the Makefile fetched. Dependencies come
# from TensorFold's own pyproject (mlx and mlx-lm on darwin, tokenizers,
# safetensors and jinja2 elsewhere).
if [ "x${USE_PIP:-}" == "xtrue" ]; then
    pip install ${EXTRA_PIP_INSTALL_FLAGS:-} "${backend_dir}/sources/TensorFold"
else
    uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} "${backend_dir}/sources/TensorFold"
fi
