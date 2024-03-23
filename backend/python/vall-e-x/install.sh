#!/bin/bash

##
## A bash script installs the required dependencies of VALL-E-X and prepares the environment
export SHA=3faaf8ccadb154d63b38070caf518ce9309ea0f4

SKIP_CONDA=${SKIP_CONDA:-0}

if [ $SKIP_CONDA -ne 1 ]; then
    source activate transformers
else
    export PATH=$PATH:/opt/conda/bin
    CONDA_PREFIX=$PWD
fi

git clone https://github.com/Plachtaa/VALL-E-X.git $CONDA_PREFIX/vall-e-x && pushd $CONDA_PREFIX/vall-e-x && git checkout -b build $SHA && popd

cp -rfv $CONDA_PREFIX/vall-e-x/* ./

if [ "$PIP_CACHE_PURGE" = true ] ; then
    pip cache purge
fi