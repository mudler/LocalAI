#!/bin/bash
set -ex

SKIP_CONDA=${SKIP_CONDA:-0}
REQUIREMENTS_FILE=$1

# Check if environment exist
conda_env_exists(){
    ! conda list --name "${@}" >/dev/null 2>/dev/null
}

if [ $SKIP_CONDA -eq 1 ]; then
    echo "Skipping conda environment installation"
else
    export PATH=$PATH:/opt/conda/bin
    if conda_env_exists "transformers" ; then
        echo "Creating virtual environment..."
        conda env create --name transformers --file $REQUIREMENTS_FILE
        echo "Virtual environment created."
    else 
        echo "Virtual environment already exists."
    fi
fi

if [ -d "/opt/intel" ]; then
    # Intel GPU: If the directory exists, we assume we are using the intel image
    # (no conda env)
    # https://github.com/intel/intel-extension-for-pytorch/issues/538
    pip install torch==2.1.0.post0 torchvision==0.16.0.post0 torchaudio==2.1.0.post0 intel-extension-for-pytorch==2.1.20+xpu oneccl_bind_pt==2.1.200+xpu intel-extension-for-transformers datasets sentencepiece tiktoken neural_speed optimum[openvino] --extra-index-url https://pytorch-extension.intel.com/release-whl/stable/xpu/us/
fi

# If we didn't skip conda, activate the environment
# to install FlashAttention
if [ $SKIP_CONDA -eq 0 ]; then
    source activate transformers
fi
if [[ $REQUIREMENTS_FILE =~ -nvidia.yml$ ]]; then
    #TODO: FlashAttention is supported on nvidia and ROCm, but ROCm install can't be done this easily
    pip install flash-attn --no-build-isolation
fi

if [ "$PIP_CACHE_PURGE" = true ] ; then
    pip cache purge
fi