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
    if conda_env_exists "parler" ; then
        echo "Creating virtual environment..."
        conda env create --name parler --file $1
        echo "Virtual environment created."
    else 
        echo "Virtual environment already exists."
    fi
fi

if [ $SKIP_CONDA -ne 1 ]; then
    # Activate conda environment
    source activate parler
    # https://github.com/descriptinc/audiotools/issues/101
    # incompatible protobuf versions.
    curl -L https://raw.githubusercontent.com/protocolbuffers/protobuf/main/python/google/protobuf/internal/builder.py -o $CONDA_PREFIX/lib/python3.11/site-packages/google/protobuf/internal/builder.py
fi

if [ "$PIP_CACHE_PURGE" = true ] ; then
    if [ $SKIP_CONDA -ne 1 ]; then
        # Activate conda environment
        source activate parler
    fi

    pip cache purge
fi