#!/bin/bash
set -ex

# Check if environment exist
conda_env_exists(){
    ! conda list --name "${@}" >/dev/null 2>/dev/null
}

if conda_env_exists "transformers" ; then
    echo "Creating virtual environment..."
    conda env create --name transformers --file transformers.yml
    echo "Virtual environment created."
else 
    echo "Virtual environment already exists."
fi
