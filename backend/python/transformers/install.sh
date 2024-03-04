#!/bin/bash
set -ex

if [ -d "/opt/intel" ]; then
    # If the directory exists, we assume we are using the intel image
    # (no conda env)
    # https://github.com/intel/intel-extension-for-pytorch/issues/538
    pip install intel-extension-for-transformers
fi

if [ "$PIP_CACHE_PURGE" = true ] ; then
    export PATH=$PATH:/opt/conda/bin

    # Activate conda environment
    source activate diffusers

    pip cache purge
fi