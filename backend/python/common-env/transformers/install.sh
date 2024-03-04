#!/bin/bash
set -ex

# Check if environment exist
conda_env_exists(){
    ! conda list --name "${@}" >/dev/null 2>/dev/null
}

if [ $SKIP_CONDA == 1 ]; then
    echo "Skipping conda environment installation"
else
    if conda_env_exists "transformers" ; then
        echo "Creating virtual environment..."
        conda env create --name transformers --file $1
        echo "Virtual environment created."
    else 
        echo "Virtual environment already exists."
    fi
fi

if [ -d "/opt/intel" ]; then
    # If the directory exists, we assume we are using the intel image
    # (no conda env)
    # https://github.com/intel/intel-extension-for-pytorch/issues/538
    pip install intel-extension-for-transformers
fi

if [ "$PIP_CACHE_PURGE" = true ] ; then
    export PATH=$PATH:/opt/conda/bin

    # Activate conda environment
    source activate transformers

    pip cache purge
fi