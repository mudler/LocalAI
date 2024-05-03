#!/bin/bash
set -ex

MY_DIR="$(dirname -- "${BASH_SOURCE[0]}")"

python -m venv ${MY_DIR}/venv
source ${MY_DIR}/venv/bin/activate

uv pip install --requirement ${MY_DIR}/requirements.txt
if [ -f "requirements-${BUILD_TYPE}.txt" ]; then
    uv pip install --requirement ${MY_DIR}/requirements-${BUILD_TYPE}.txt
fi

# https://github.com/descriptinc/audiotools/issues/101
# incompatible protobuf versions.
curl -L https://raw.githubusercontent.com/protocolbuffers/protobuf/main/python/google/protobuf/internal/builder.py -o $MY_DIR/venv/lib/python3.11/site-packages/google/protobuf/internal/builder.py

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