#!/bin/bash

##
## A bash script installs the required dependencies of VALL-E-X and prepares the environment
export PATH=$PATH:/opt/conda/bin

# Activate conda environment
source activate ttsvalle

echo $CONDA_PREFIX

git clone https://github.com/Plachtaa/VALL-E-X.git $CONDA_PREFIX/vall-e-x && pushd $CONDA_PREFIX/vall-e-x && pip install -r requirements.txt && popd

cp -rfv $CONDA_PREFIX/vall-e-x/* ./