#!/bin/bash
set -e

source $(dirname $0)/../common/libbackend.sh

# This is here because the Intel pip index is broken and returns 200 status codes for every package name, it just doesn't return any package links.
# This makes uv think that the package exists in the Intel pip index, and by default it stops looking at other pip indexes once it finds a match.
# We need uv to continue falling through to the pypi default index to find optimum[openvino] in the pypi index
# the --upgrade actually allows us to *downgrade* torch to the version provided in the Intel pip index
if [ "x${BUILD_PROFILE}" == "xintel" ]; then
    EXTRA_PIP_INSTALL_FLAGS+=" --upgrade --index-strategy=unsafe-first-match"
fi


installRequirements


# https://github.com/descriptinc/audiotools/issues/101
# incompatible protobuf versions.
PYDIR=python3.10
pyenv="${MY_DIR}/venv/lib/${PYDIR}/site-packages/google/protobuf/internal/"

if [ ! -d ${pyenv} ]; then
    echo "(parler-tts/install.sh): Error: ${pyenv} does not exist"
    exit 1
fi

curl -L https://raw.githubusercontent.com/protocolbuffers/protobuf/main/python/google/protobuf/internal/builder.py -o ${pyenv}/builder.py
