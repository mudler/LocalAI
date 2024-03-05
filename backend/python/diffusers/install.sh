#!/bin/bash
set -ex

SKIP_CONDA=${SKIP_CONDA:-0}

# Check if environment exist
conda_env_exists(){
    ! conda list --name "${@}" >/dev/null 2>/dev/null
}

if [ $SKIP_CONDA -eq 1 ]; then
    echo "Skipping conda environment installation"
else
    export PATH=$PATH:/opt/conda/bin
    if conda_env_exists "diffusers" ; then
        echo "Creating virtual environment..."
        conda env create --name diffusers --file $1
        echo "Virtual environment created."
    else 
        echo "Virtual environment already exists."
    fi
fi

if [ -d "/opt/intel" ]; then
    # Intel GPU: If the directory exists, we assume we are using the Intel image
    # https://github.com/intel/intel-extension-for-pytorch/issues/538
    pip install torch==2.1.0a0 \
                torchvision==0.16.0a0 \
                torchaudio==2.1.0a0 \
                intel-extension-for-pytorch==2.1.10+xpu \
                --extra-index-url https://pytorch-extension.intel.com/release-whl/stable/xpu/us/
    
    pip install google-api-python-client \
                grpcio \
                grpcio-tools \
                diffusers==0.24.0 \
                transformers>=4.25.1 \
                accelerate \
                compel==2.0.2 \
                Pillow
fi

if [ "$PIP_CACHE_PURGE" = true ] ; then
    if [ $SKIP_CONDA -ne 1 ]; then
        # Activate conda environment
        source activate diffusers
    fi

    pip cache purge
fi