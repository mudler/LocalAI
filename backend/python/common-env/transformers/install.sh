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
    if conda_env_exists "transformers" ; then
        echo "Creating virtual environment..."
        conda env create --name transformers --file $1
        echo "Virtual environment created."
    else 
        echo "Virtual environment already exists."
    fi
fi

if [ -d "/opt/intel" ]; then
    # Intel GPU: If the directory exists, we assume we are using the intel image
    # (no conda env)
    # https://github.com/intel/intel-extension-for-pytorch/issues/538
    pip install intel-extension-for-transformers datasets sentencepiece tiktoken neural_speed
fi

if [ "$PIP_CACHE_PURGE" = true ] ; then
    if [ $SKIP_CONDA -eq 0 ]; then
        # Activate conda environment
        source activate transformers
    fi

    pip cache purge
fi