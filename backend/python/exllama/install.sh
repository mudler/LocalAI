#!/bin/bash
set -ex

export PATH=$PATH:/opt/conda/bin

if [ "$BUILD_TYPE" != "cublas" ]; then
    echo "[exllama] Attention!!! Nvidia GPU is required - skipping installation"
    exit 0
fi

# Check if environment exist
conda_env_exists(){
    ! conda list --name "${@}" >/dev/null 2>/dev/null
}

if conda_env_exists "exllama" ; then
    echo "Creating virtual environment..."
    conda env create --name exllama --file $1
    echo "Virtual environment created."
else
    echo "Virtual environment already exists."
fi

source activate exllama

git clone https://github.com/turboderp/exllama $CONDA_PREFIX/exllama && pushd $CONDA_PREFIX/exllama && pip install -r requirements.txt && popd

cp -rfv $CONDA_PREFIX/exllama/* ./

if [ "$PIP_CACHE_PURGE" = true ] ; then
    pip cache purge
fi