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
    pip install --upgrade google-api-python-client
    pip install --upgrade grpcio
    pip install --upgrade grpcio-tools
    pip install diffusers==0.24.0
    pip install transformers>=4.25.1
    pip install accelerate
    pip install compel==2.0.2
fi

if [ "$PIP_CACHE_PURGE" = true ] ; then
    export PATH=$PATH:/opt/conda/bin

    # Activate conda environment
    source activate diffusers

    pip cache purge
fi