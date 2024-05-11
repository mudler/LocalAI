#!/bin/bash
set -e

VALL_E_X_VERSION=3faaf8ccadb154d63b38070caf518ce9309ea0f4

source $(dirname $0)/../common/libbackend.sh

installRequirements

git clone https://github.com/Plachtaa/VALL-E-X.git ${MY_DIR}/source
pushd ${MY_DIR}/source && git checkout -b build ${VALL_E_X_VERSION} && popd
uv pip install ${BUILD_ISOLATION_FLAG} --requirement ${MY_DIR}/source/requirements.txt

cp -v ./*py $MY_DIR/source/
