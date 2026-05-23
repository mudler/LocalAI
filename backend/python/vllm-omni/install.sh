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
# Since PyTorch 2.11 (April 2026) PyPI ships aarch64 + cu130 manylinux wheels
# directly for torch/torchvision/torchaudio and an aarch64 vllm wheel pinned
# to that torch, so the jetson-ai-lab mirror is no longer needed.
# https://pytorch.org/blog/vllm-and-pytorch-work-together-to-improve-the-developer-experience-on-aarch64/
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
elif [ "x${BUILD_PROFILE}" == "xcublas13" ] || [ "x${BUILD_PROFILE}" == "xl4t13" ]; then
    # cublas13 (x86_64) and l4t13 (aarch64) both pull vllm from PyPI now:
    # vllm 0.19+ defaults to cu130 wheels on x86_64 and vllm 0.20+ ships an
    # aarch64 manylinux wheel pinned to torch==2.11.0. No extra index needed
    # in either case.
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

# fa3-fwd ships no aarch64 wheels and there is no source distribution, so on
# aarch64 (e.g. l4t13 / SBSA cu130) the upstream requirements/cuda.txt is
# unsatisfiable. Drop it before resolving — vllm-omni does not hard-require
# the fused FA3 kernel at import time on Jetson/SBSA targets.
if [ "$(uname -m)" = "aarch64" ] && [ -f requirements/cuda.txt ]; then
    sed -i '/^fa3-fwd[[:space:]]*==/d' requirements/cuda.txt
fi

if [ "x${USE_PIP}" == "xtrue" ]; then
    pip install ${EXTRA_PIP_INSTALL_FLAGS:-} -e .
else
    uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} -e .
fi

cd ..
