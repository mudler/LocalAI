#!/bin/bash
set -ex

# Check if environment exist
conda_env_exists(){
    ! conda list --name "${@}" >/dev/null 2>/dev/null
}

if conda_env_exists "diffusers" ; then
    echo "Creating virtual environment..."
    conda env create --name diffusers --file $1
    echo "Virtual environment created."
else 
    echo "Virtual environment already exists."
fi

if [ -d "/opt/intel" ]; then
    # If the directory exists, we assume we are using the intel image
    # https://github.com/intel/intel-extension-for-pytorch/issues/538
    python -m pip install torch==2.0.1a0 torchvision==0.15.2a0 intel-extension-for-pytorch==2.0.120+xpu --extra-index-url https://pytorch-extension.intel.com/release-whl-aitools/
    pip install google-api-python-client \
                grpcio \
                grpcio-tools \
                diffusers==0.24.0 \
                transformers>=4.25.1 \
                accelerate \
                compel==2.0.2
fi

if [ "$PIP_CACHE_PURGE" = true ] ; then
    export PATH=$PATH:/opt/conda/bin

    # Activate conda environment
    source activate diffusers

    pip cache purge
fi