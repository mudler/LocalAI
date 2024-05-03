#!/bin/bash
set -ex

##
## A bash script installs the required dependencies of VALL-E-X and prepares the environment
export VALL_E_X_VERSION=3faaf8ccadb154d63b38070caf518ce9309ea0f4
MY_DIR="$(dirname -- "${BASH_SOURCE[0]}")"

python -m venv ${MY_DIR}/venv
source ${MY_DIR}/venv/bin/activate

uv pip install --requirement ${MY_DIR}/requirements.txt

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

git clone https://github.com/Plachtaa/VALL-E-X.git $MY_DIR/source
pushd $MY_DIR/source && git checkout -b build $VALL_E_X_VERSION && popd

uv pip install --requirement source/requirements.txt

cp -rfv ./*py $MY_DIR/source/  

if [ "$PIP_CACHE_PURGE" = true ] ; then
    pip cache purge
fi