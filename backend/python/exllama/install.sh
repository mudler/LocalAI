#!/bin/bash
set -e

LIMIT_TARGETS="cublas"

source $(dirname $0)/../common/libbackend.sh

installRequirements

git clone https://github.com/turboderp/exllama $MY_DIR/source
uv pip install ${BUILD_ISOLATION_FLAG} --requirement ${MY_DIR}/source/requirements.txt

cp -v ./*py $MY_DIR/source/
