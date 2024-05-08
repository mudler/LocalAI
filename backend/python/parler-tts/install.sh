#!/bin/bash
set -ex

BUILD_ISOLATION_FLAG=""

MY_DIR="$(dirname -- "${BASH_SOURCE[0]}")"

uv venv ${MY_DIR}/venv
source ${MY_DIR}/venv/bin/activate

if [ -f "requirements-install.txt" ]; then
    # If we have a requirements-install.txt, it means that a package does not properly declare it's build time
    # dependencies per PEP-517, so we have to set up the proper build environment ourselves, and then install
    # the package without build isolation
    BUILD_ISOLATION_FLAG="--no-build-isolation"
    uv pip install --requirement ${MY_DIR}/requirements-install.txt
fi
uv pip install ${BUILD_ISOLATION_FLAG} --requirement ${MY_DIR}/requirements.txt

if [ -f "requirements-${BUILD_TYPE}.txt" ]; then
    uv pip install ${BUILD_ISOLATION_FLAG}  --requirement ${MY_DIR}/requirements-${BUILD_TYPE}.txt
fi

if [ -d "/opt/intel" ]; then
    # Intel GPU: If the directory exists, we assume we are using the Intel image
    # https://github.com/intel/intel-extension-for-pytorch/issues/538
    if [ -f "requirements-intel.txt" ]; then
        uv pip install ${BUILD_ISOLATION_FLAG}  --index-url https://pytorch-extension.intel.com/release-whl/stable/xpu/us/ --requirement ${MY_DIR}/requirements-intel.txt
    fi
fi

# https://github.com/descriptinc/audiotools/issues/101
# incompatible protobuf versions.
PYDIR=$(ls $MY_DIR/venv/lib)
curl -L https://raw.githubusercontent.com/protocolbuffers/protobuf/main/python/google/protobuf/internal/builder.py -o $MY_DIR/venv/lib/$PYDIR/site-packages/google/protobuf/internal/builder.py

if [ "$PIP_CACHE_PURGE" = true ] ; then
    pip cache purge
fi