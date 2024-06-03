#!/bin/bash
set -e

VALL_E_X_VERSION=3faaf8ccadb154d63b38070caf518ce9309ea0f4

source $(dirname $0)/../common/libbackend.sh

# This is here because the Intel pip index is broken and returns 200 status codes for every package name, it just doesn't return any package links.
# This makes uv think that the package exists in the Intel pip index, and by default it stops looking at other pip indexes once it finds a match.
# We need uv to continue falling through to the pypi default index to find optimum[openvino] in the pypi index
# the --upgrade actually allows us to *downgrade* torch to the version provided in the Intel pip index
if [ "x${BUILD_PROFILE}" == "xintel" ]; then
    EXTRA_PIP_INSTALL_FLAGS+=" --upgrade --index-strategy=unsafe-first-match"
fi

installRequirements

git clone https://github.com/Plachtaa/VALL-E-X.git ${MY_DIR}/source
pushd ${MY_DIR}/source && git checkout -b build ${VALL_E_X_VERSION} && popd
uv pip install ${BUILD_ISOLATION_FLAG} --requirement ${MY_DIR}/source/requirements.txt

cp -v ./*py $MY_DIR/source/
