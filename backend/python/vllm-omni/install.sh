#!/bin/bash
set -e

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

# Handle l4t build profiles (Python 3.12, pip fallback) if needed
if [ "x${BUILD_PROFILE}" == "xl4t13" ]; then
  PYTHON_VERSION="3.12"
  PYTHON_PATCH="12"
  PY_STANDALONE_TAG="20251120"
fi

if [ "x${BUILD_PROFILE}" == "xl4t12" ]; then
    USE_PIP=true
fi

# Install base requirements first
installRequirements

# Install vllm based on build type
if [ "x${BUILD_TYPE}" == "xhipblas" ]; then
    # ROCm
    if [ "x${USE_PIP}" == "xtrue" ]; then
        pip install vllm==0.14.0 --extra-index-url https://wheels.vllm.ai/rocm/0.14.0/rocm700
    else
        uv pip install vllm==0.14.0 --extra-index-url https://wheels.vllm.ai/rocm/0.14.0/rocm700
    fi
elif [ "x${BUILD_TYPE}" == "xcublas" ] || [ "x${BUILD_TYPE}" == "x" ]; then
    # CUDA (default) or CPU
    if [ "x${USE_PIP}" == "xtrue" ]; then
        pip install vllm==0.14.0 --torch-backend=auto
    else
        uv pip install vllm==0.14.0 --torch-backend=auto
    fi
else
    echo "Unsupported build type: ${BUILD_TYPE}" >&2
    exit 1
fi

# Clone and install vllm-omni from source
if [ ! -d vllm-omni ]; then
    git clone https://github.com/vllm-project/vllm-omni.git
fi

cd vllm-omni/

if [ "x${USE_PIP}" == "xtrue" ]; then
    pip install ${EXTRA_PIP_INSTALL_FLAGS:-} -e .
else
    uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} -e .
fi

cd ..
