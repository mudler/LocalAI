#!/bin/bash
set -e

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

# This is here because the Intel pip index is broken and returns 200 status codes for every package name, it just doesn't return any package links.
# This makes uv think that the package exists in the Intel pip index, and by default it stops looking at other pip indexes once it finds a match.
# We need uv to continue falling through to the pypi default index to find optimum[openvino] in the pypi index
# the --upgrade actually allows us to *downgrade* torch to the version provided in the Intel pip index
if [ "x${BUILD_PROFILE}" == "xintel" ]; then
    EXTRA_PIP_INSTALL_FLAGS+=" --upgrade --index-strategy=unsafe-first-match"
fi

if [ "x${BUILD_TYPE}" == "xcublas" ] || [ "x${BUILD_TYPE}" == "xl4t" ]; then
    export CMAKE_ARGS="-DGGML_CUDA=on"
fi

if [ "x${BUILD_TYPE}" == "xhipblas" ]; then
    export CMAKE_ARGS="-DGGML_HIPBLAS=on"
fi

EXTRA_PIP_INSTALL_FLAGS+=" --no-build-isolation"

git clone https://github.com/neuphonic/neutts-air neutts-air

cp -rfv neutts-air/neuttsair ./

installRequirements
