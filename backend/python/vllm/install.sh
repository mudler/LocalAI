#!/bin/bash
set -e

EXTRA_PIP_INSTALL_FLAGS="--no-build-isolation"

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

# We don't embed this into the images as it is a large dependency and not always needed.
# Besides, the speed inference are not actually usable in the current state for production use-cases.
if [ "x${BUILD_TYPE}" == "x" ] && [ "x${FROM_SOURCE}" == "xtrue" ]; then
        ensureVenv
        # https://docs.vllm.ai/en/v0.6.1/getting_started/cpu-installation.html
        if [ ! -d vllm ]; then
            git clone https://github.com/vllm-project/vllm
        fi
        pushd vllm
            uv pip install --upgrade pip
            uv pip install -v -r requirements/cpu-build.txt --extra-index-url https://download.pytorch.org/whl/cpu --index-strategy unsafe-best-match
            uv pip install -v -r requirements/cpu.txt --extra-index-url https://download.pytorch.org/whl/cpu --index-strategy unsafe-best-match
            uv pip install numpy typing-extensions pillow grpcio==1.71.0 protobuf protobuf certifi accelerate transformers bitsandbytes
            VLLM_TARGET_DEVICE=cpu python setup.py install
        popd
        rm -rf vllm
    else
        installRequirements
fi
