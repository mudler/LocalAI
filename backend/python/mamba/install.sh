#!/bin/bash

##
## A bash script installs the required dependencies of VALL-E-X and prepares the environment
export PATH=$PATH:/opt/conda/bin

if [ "$BUILD_TYPE" != "cublas" ]; then
    echo "[mamba] Attention!!! nvcc is required - skipping installation"
    exit 0
fi

# Activate conda environment
source activate transformers

echo $CONDA_PREFIX

pip install causal-conv1d==1.0.0 mamba-ssm==1.0.1

if [ "$PIP_CACHE_PURGE" = true ] ; then
    pip cache purge
fi