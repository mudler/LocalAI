#!/bin/bash

##
## A bash script installs the required dependencies of VALL-E-X and prepares the environment
export PATH=$PATH:/opt/conda/bin

# Activate conda environment
source activate exllama2

echo $CONDA_PREFIX

git clone https://github.com/turboderp/exllamav2 $CONDA_PREFIX/exllamav2 && pushd $CONDA_PREFIX/exllamav2 && pip install -r requirements.txt && popd

cp -rfv $CONDA_PREFIX/exllamav2/* ./  