#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath "$0")")

# The colibri release (engines and the coli gateway) and the Python that runs
# coli ship inside this backend. The model options coli_path and python
# still override them.
export COLIBRI_COLI="${COLIBRI_COLI:-$CURDIR/colibri-dist/coli}"
export COLIBRI_PYTHON="${COLIBRI_PYTHON:-$CURDIR/python/bin/python3}"

# lib/ holds the OpenMP runtime the engines link (and the Vulkan loader in
# the Vulkan image). The variable reaches coli and the engines it starts.
if [ "$(uname)" = "Darwin" ]; then
	export DYLD_LIBRARY_PATH="$CURDIR/lib${DYLD_LIBRARY_PATH:+:$DYLD_LIBRARY_PATH}"
else
	export LD_LIBRARY_PATH="$CURDIR/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
fi

# The Vulkan image turns colibri's Vulkan backend on unless the model config
# says otherwise (env: COLI_VULKAN: "0"). The engines fall back to the CPU
# when no device is found.
if [ -f "$CURDIR/colibri-vulkan" ]; then
	export COLI_VULKAN="${COLI_VULKAN:-1}"
fi

# The CUDA image's engines are built with colibri's CUDA backend, which is off
# unless asked for. Turn it on the way colibri's own docs do (docs/cuda.md):
# dense layers on the GPU and an expert cache that fills the free VRAM.
# DeepSeek V4's engine reads DSV4_CUDA instead. The model config's env wins.
if [ -f "$CURDIR/colibri-cuda" ]; then
	export COLI_CUDA="${COLI_CUDA:-1}"
	export CUDA_DENSE="${CUDA_DENSE:-1}"
	export CUDA_EXPERT_GB="${CUDA_EXPERT_GB:-auto}"
	export DSV4_CUDA="${DSV4_CUDA:-1}"
fi

exec "$CURDIR"/colibri "$@"
