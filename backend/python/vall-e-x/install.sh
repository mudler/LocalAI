#!/bin/bash

##
## A bash script installs the required dependencies of VALL-E-X and prepares the environment
export PATH=$PATH:/opt/conda/bin
export SHA=3faaf8ccadb154d63b38070caf518ce9309ea0f4

# Activate conda environment
source activate transformers

echo $CONDA_PREFIX

git clone https://github.com/Plachtaa/VALL-E-X.git $CONDA_PREFIX/vall-e-x && pushd $CONDA_PREFIX/vall-e-x && git checkout -b build $SHA && pip install -r requirements.txt && popd

cp -rfv $CONDA_PREFIX/vall-e-x/* ./

if [ "$PIP_CACHE_PURGE" = true ] ; then
    pip cache purge
fi