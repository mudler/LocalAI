#!/bin/bash
set -e

PYTHON_VERSION="3.12"
PYTHON_PATCH="12"
PY_STANDALONE_TAG="20251120"

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

# Handle l4t build profiles (Python 3.12, pip fallback) if needed.
# unsafe-best-match is required on l4t13 because the jetson-ai-lab index
# lists transitive deps at limited versions — without it uv pins to the
# first matching index and fails to resolve a compatible wheel from PyPI.
if [ "x${BUILD_PROFILE}" == "xl4t13" ]; then
  PYTHON_VERSION="3.12"
  PYTHON_PATCH="12"
  PY_STANDALONE_TAG="20251120"
  EXTRA_PIP_INSTALL_FLAGS="${EXTRA_PIP_INSTALL_FLAGS:-} --index-strategy=unsafe-best-match"
fi

if [ "x${BUILD_PROFILE}" == "xl4t12" ]; then
    USE_PIP=true
fi

# Install base requirements first
installRequirements

# Install vllm based on build type. vllm-omni tracks vllm master from
# source (cloned below) so we leave the upstream vllm dependency unpinned
# — vllm 0.19+ ships cu130 wheels by default, which is what we want for
# cublas13. Older cuda12/rocm/cpu paths still resolve a compatible wheel
# from the relevant channel.
if [ "x${BUILD_TYPE}" == "xhipblas" ]; then
    # ROCm
    if [ "x${USE_PIP}" == "xtrue" ]; then
        pip install vllm==0.14.0 --extra-index-url https://wheels.vllm.ai/rocm/0.14.0/rocm700
    else
        uv pip install vllm==0.14.0 --extra-index-url https://wheels.vllm.ai/rocm/0.14.0/rocm700
    fi
elif [ "x${BUILD_PROFILE}" == "xl4t13" ]; then
    # JetPack 7 / L4T arm64 cu130 — vllm comes from the prebuilt SBSA wheel
    # at jetson-ai-lab. Version is unpinned: the index ships whatever build
    # matches the cu130/cp312 ABI. unsafe-best-match lets uv fall through
    # to PyPI for transitive deps not present on the jetson-ai-lab index.
    if [ "x${USE_PIP}" == "xtrue" ]; then
        pip install vllm --extra-index-url https://pypi.jetson-ai-lab.io/sbsa/cu130
    else
        uv pip install --index-strategy=unsafe-best-match vllm --extra-index-url https://pypi.jetson-ai-lab.io/sbsa/cu130
    fi
elif [ "x${BUILD_PROFILE}" == "xcublas13" ]; then
    # vllm 0.19+ defaults to cu130 wheels on PyPI, no extra index needed.
    if [ "x${USE_PIP}" == "xtrue" ]; then
        pip install vllm --torch-backend=auto
    else
        uv pip install vllm --torch-backend=auto
    fi
elif [ "x${BUILD_TYPE}" == "xcublas" ] || [ "x${BUILD_TYPE}" == "x" ]; then
    # cuda12 / CPU — keep the 0.14.0 pin for compatibility with the existing
    # cuda12 vllm-omni image; bumping should be its own change.
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
