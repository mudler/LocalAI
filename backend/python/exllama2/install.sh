#!/bin/bash
set -e
##
## A bash script installs the required dependencies of VALL-E-X and prepares the environment
EXLLAMA2_VERSION=c0ddebaaaf8ffd1b3529c2bb654e650bce2f790f

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

git clone https://github.com/turboderp/exllamav2 $MY_DIR/source
pushd ${MY_DIR}/source && git checkout -b build ${EXLLAMA2_VERSION} && popd

uv pip install ${BUILD_ISOLATION_FLAG} --requirement ${MY_DIR}/source/requirements.txt
# This installs exllamav2 in JIT mode so it will compile the appropriate torch extension at runtime
EXLLAMA_NOCOMPILE= uv pip install ${BUILD_ISOLATION_FLAG} ${MY_DIR}/source/

cp -rfv ./*py $MY_DIR/source/

if [ "$PIP_CACHE_PURGE" = true ] ; then
    pip cache purge
fi