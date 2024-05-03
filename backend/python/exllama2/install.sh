#!/bin/bash
set -e
##
## A bash script installs the required dependencies of VALL-E-X and prepares the environment
EXLLAMA2_VERSION=c0ddebaaaf8ffd1b3529c2bb654e650bce2f790f

if [ "$BUILD_TYPE" != "cublas" ]; then
    echo "[exllamav2] Attention!!! Nvidia GPU is required - skipping installation"
    exit 0
fi

MY_DIR="$(dirname -- "${BASH_SOURCE[0]}")"

python -m venv ${MY_DIR}/venv
source ${MY_DIR}/venv/bin/activate

uv pip install --requirement ${MY_DIR}/requirements.txt

if [ -f "requirements-${BUILD_TYPE}.txt" ]; then
    uv pip install --requirement ${MY_DIR}/requirements-${BUILD_TYPE}.txt
fi

git clone https://github.com/turboderp/exllamav2 source
pushd source && git checkout -b build ${EXLLAMA2_VERSION} && popd

uv pip install --requirement source/requirements.txt

cp -rfv ./*py $MY_DIR/source/  

if [ "$PIP_CACHE_PURGE" = true ] ; then
    pip cache purge
fi