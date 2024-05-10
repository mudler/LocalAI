#!/bin/bash
set -ex

BUILD_ISOLATION_FLAG=""

if [ "$BUILD_TYPE" != "cublas" ]; then
    echo "[exllama] Attention!!! Nvidia GPU is required - skipping installation"
    exit 0
fi

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
    uv pip install ${BUILD_ISOLATION_FLAG} --requirement ${MY_DIR}/requirements-${BUILD_TYPE}.txt
fi

git clone https://github.com/turboderp/exllama $MY_DIR/source
uv pip install ${BUILD_ISOLATION_FLAG} --requirement ${MY_DIR}/source/requirements.txt

cp -rfv ./*py $MY_DIR/source/

if [ "$PIP_CACHE_PURGE" = true ] ; then
    pip cache purge
fi