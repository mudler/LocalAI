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

exec "$CURDIR"/colibri "$@"
