#!/bin/bash
set -ex

if [ "$BUILD_TYPE" != "cublas" ]; then
    echo "[exllama] Attention!!! Nvidia GPU is required - skipping installation"
    exit 0
fi

MY_DIR="$(dirname -- "${BASH_SOURCE[0]}")"

python -m venv ${MY_DIR}/venv
source ${MY_DIR}/venv/bin/activate

uv pip install --requirement ${MY_DIR}/requirements.txt

if [ -f "requirements-${BUILD_TYPE}.txt" ]; then
    uv pip install --requirement ${MY_DIR}/requirements-${BUILD_TYPE}.txt
fi

git clone https://github.com/turboderp/exllama $MY_DIR/source && pushd $MY_DIR/source && uv pip install --requirement requirements.txt && popd

cp -rfv ./*py $MY_DIR/source/

if [ "$PIP_CACHE_PURGE" = true ] ; then
    pip cache purge
fi