#!/bin/bash

##
## A bash script installs the required dependencies of VALL-E-X and prepares the environment
export PATH=$PATH:/opt/conda/bin

# Activate conda environment
source activate transformers

echo $CONDA_PREFIX


git clone https://github.com/turboderp/exllama $CONDA_PREFIX/exllama && pushd $CONDA_PREFIX/exllama && pip install -r requirements.txt && popd

cp -rfv $CONDA_PREFIX/exllama/* ./

if [ "$PIP_CACHE_PURGE" = true ] ; then
    pip cache purge
fi