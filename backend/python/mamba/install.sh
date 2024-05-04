#!/bin/bash
set -e
##
## A bash script installs the required dependencies of VALL-E-X and prepares the environment

if [ "$BUILD_TYPE" != "cublas" ]; then
    echo "[mamba] Attention!!! nvcc is required - skipping installation"
    exit 0
fi

MY_DIR="$(dirname -- "${BASH_SOURCE[0]}")"

uv venv ${MY_DIR}/venv
source ${MY_DIR}/venv/bin/activate

# mabma does not specify it's build dependencies per PEP517, so we need to disable build isolation
# this also means that we need to install the basic build dependencies into the venv ourselves
# https://github.com/Dao-AILab/causal-conv1d/issues/24
uv pip install --requirement ${MY_DIR}/requirements-install.txt

uv pip install --no-build-isolation --requirement ${MY_DIR}/requirements.txt

if [ -f "requirements-${BUILD_TYPE}.txt" ]; then
    uv pip install --requirement ${MY_DIR}/requirements-${BUILD_TYPE}.txt
fi

if [ -d "/opt/intel" ]; then
    # Intel GPU: If the directory exists, we assume we are using the Intel image
    # https://github.com/intel/intel-extension-for-pytorch/issues/538
    if [ -f "requirements-intel.txt" ]; then
        uv pip install --index-url https://pytorch-extension.intel.com/release-whl/stable/xpu/us/ --requirement ${MY_DIR}/requirements-intel.txt
    fi
fi

if [ "$PIP_CACHE_PURGE" = true ] ; then
    pip cache purge
fi